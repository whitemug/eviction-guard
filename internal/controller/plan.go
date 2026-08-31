/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/backends"
	"github.com/whitemug/eviction-guard/pkg/capacity"
	"github.com/whitemug/eviction-guard/pkg/metrics"
)

// scalePlan is the fan-out for one disruption: what to patch, to which value,
// with which stamps. Apply and Restore are the only mutation paths.
type scalePlan struct {
	steps           []scaleStep
	primaryBaseline int32
	desired         int32
}

type scaleStep struct {
	backend   egv1a1.ScaleBackendType
	target    backends.Target
	baseline  int32
	desired   int32
	stamp     map[string]string
	stampKeys []string
}

func buildPlan(ctx context.Context, c client.Client, policy *egv1a1.EvictionGuardPolicy, dep *appsv1.Deployment, atRisk int32, existing []egv1a1.ScaleAction) (*scalePlan, error) {
	types, err := backendTypes(dep, policy)
	if err != nil {
		return nil, err
	}
	if len(types) == 0 {
		return nil, fmt.Errorf("no scale backends configured")
	}
	types = backends.SortForScaleUp(types)

	stamp := resolveStamp(policy, dep)
	plan := &scalePlan{}
	for i, bt := range types {
		target, err := resolveActionTarget(dep, bt)
		if err != nil {
			return nil, err
		}
		backend, err := backends.Get(bt)
		if err != nil {
			return nil, err
		}
		current, err := backend.Current(ctx, c, target)
		if err != nil {
			return nil, err
		}
		baseline := current
		if old := findAction(existing, bt, target); old != nil {
			baseline = old.Baseline
		}
		if i == 0 {
			plan.primaryBaseline = baseline
			plan.desired = capacity.TargetReplicas(baseline, atRisk, policy.SpareReplicasOrDefault(), policy.MaxBufferOrDefault())
		}
		plan.steps = append(plan.steps, scaleStep{
			backend:   bt,
			target:    target,
			baseline:  baseline,
			desired:   plan.desired,
			stamp:     stamp.values(baseline, plan.desired),
			stampKeys: stamp.keys(),
		})
	}
	return plan, nil
}

func (p *scalePlan) apply(ctx context.Context, c client.Client, policyName string) (bool, error) {
	scaled := false
	for _, s := range p.steps {
		backend, err := backends.Get(s.backend)
		if err != nil {
			return scaled, err
		}
		current, err := backend.Current(ctx, c, s.target)
		if err != nil {
			return scaled, err
		}
		if current < s.desired {
			if err := backend.ScaleUp(ctx, c, s.target, s.desired); err != nil {
				return scaled, err
			}
			metrics.ScaleActions.WithLabelValues(policyName, "up", string(s.backend), "ok").Inc()
			scaled = true
		}
		if err := backends.PatchAnnotations(ctx, c, s.target, s.stamp); err != nil {
			return scaled, err
		}
	}
	return scaled, nil
}

func (p *scalePlan) actions(existing []egv1a1.ScaleAction) []egv1a1.ScaleAction {
	var next []egv1a1.ScaleAction
	for _, s := range p.steps {
		next = append(next, scaleAction(s.backend, s.target, s.baseline, s.desired, s.stampKeys))
	}
	return mergeActions(existing, next)
}

func (p *scalePlan) primary() scaleStep { return p.steps[0] }

func restoreActions(ctx context.Context, c client.Client, win *egv1a1.EvictionGuardWindow, deployFloor int32) error {
	actions := backends.SortActionsForScaleDown(win.ScaleActions())
	coord := hasBackend(actions, egv1a1.ScaleBackendDeployment) && hasBackend(actions, egv1a1.ScaleBackendHPAMin)
	for _, a := range actions {
		backend, err := backends.Get(a.Backend)
		if err != nil {
			return err
		}
		target := actionTarget(a)
		if target.Namespace == "" {
			target.Namespace = win.Namespace
		}
		current, err := backend.Current(ctx, c, target)
		if err != nil {
			return err
		}
		floor := a.Baseline
		if a.Backend == egv1a1.ScaleBackendDeployment {
			floor = deployFloor
		}
		if current > floor {
			if a.Backend == egv1a1.ScaleBackendHPAMin && coord {
				if err := backends.RestoreMinReplicas(ctx, c, target, floor); err != nil {
					return err
				}
			} else if err := backend.ScaleDown(ctx, c, target, floor); err != nil {
				return err
			}
		}
		if err := backends.PatchAnnotations(ctx, c, target, backends.StampDeletes(a.StampKeys)); err != nil {
			return err
		}
	}
	return nil
}

func workloadDeployment(ctx context.Context, c client.Client, ns, name string) (*appsv1.Deployment, error) {
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, dep); client.IgnoreNotFound(err) != nil {
		return nil, err
	}
	return dep, nil
}

func backendTypes(dep *appsv1.Deployment, policy *egv1a1.EvictionGuardPolicy) ([]egv1a1.ScaleBackendType, error) {
	if dep.Annotations != nil {
		if v := dep.Annotations[egv1a1.ScaleBackendAnnotation]; v != "" {
			return backends.ParseList(v)
		}
	}
	return policy.BackendsOrDefault(), nil
}

func backendLabel(dep *appsv1.Deployment, policy *egv1a1.EvictionGuardPolicy) string {
	types, err := backendTypes(dep, policy)
	if err != nil || len(types) == 0 {
		return string(policy.DefaultBackendOrDefault())
	}
	parts := make([]string, len(types))
	for i, t := range types {
		parts[i] = string(t)
	}
	return strings.Join(parts, ",")
}

// stampCooldown writes or clears the window-until annotation on every scaled object.
// A zero until deletes the key (Open again); a real time is written when Cooling starts.
func stampCooldown(ctx context.Context, c client.Client, policy *egv1a1.EvictionGuardPolicy, dep *appsv1.Deployment, win *egv1a1.EvictionGuardWindow, until time.Time) error {
	st := resolveStamp(policy, dep)
	if !st.enabled {
		return nil
	}
	anns := map[string]string{st.windowUntil: ""}
	if !until.IsZero() {
		anns[st.windowUntil] = until.UTC().Format(time.RFC3339)
	}
	for _, a := range win.ScaleActions() {
		t := actionTarget(a)
		if t.Namespace == "" {
			t.Namespace = win.Namespace
		}
		if err := backends.PatchAnnotations(ctx, c, t, anns); err != nil {
			return err
		}
	}
	return nil
}
