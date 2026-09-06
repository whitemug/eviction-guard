/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	key       string
	target    backends.Target
	baseline  int32
	desired   int32
	stamp     map[string]string
	stampKeys []string
}

func buildPlan(ctx context.Context, c client.Client, policy *egv1a1.EvictionGuardPolicy, dep *appsv1.Deployment, atRisk int32, existing []egv1a1.ScaleAction, stickyBaseline int32) (*scalePlan, error) {
	if policy == nil || len(policy.Spec.Backends) == 0 {
		return nil, fmt.Errorf("policy.spec.backends is required")
	}
	bindings, err := backends.BindingsForWorkload(dep, policy)
	if err != nil {
		return nil, err
	}
	resolved, err := backends.ResolveCatalog(dep, policy, bindings)
	if err != nil {
		return nil, err
	}
	stamp := resolveStamp(policy, dep)
	plan := &scalePlan{}

	if len(resolved) == 0 {
		// All bound catalog entries are external (no path patches): Window + metrics only.
		baseline := stickyBaseline
		if baseline <= 0 {
			baseline = deployReplicas(dep)
		}
		plan.primaryBaseline = baseline
		plan.desired = capacity.TargetReplicas(baseline, atRisk, policy.SpareReplicasOrDefault(), policy.MaxBufferOrDefault())
		return plan, nil
	}

	for i, step := range resolved {
		current, err := backends.Current(ctx, c, step.Target)
		if err != nil {
			return nil, err
		}
		baseline := current
		if old := findAction(existing, step.Key, step.Target); old != nil {
			baseline = old.Baseline
		}
		if i == 0 {
			plan.primaryBaseline = baseline
			plan.desired = capacity.TargetReplicas(baseline, atRisk, policy.SpareReplicasOrDefault(), policy.MaxBufferOrDefault())
		}
		anns := stamp.values(baseline, plan.desired)
		keys := stamp.keys()
		for _, a := range step.Annotations {
			if anns == nil {
				anns = map[string]string{}
			}
			anns[a] = "true"
			keys = append(keys, a)
		}
		plan.steps = append(plan.steps, scaleStep{
			key:       step.Key,
			target:    step.Target,
			baseline:  baseline,
			desired:   plan.desired,
			stamp:     anns,
			stampKeys: uniqueStrings(keys),
		})
	}
	return plan, nil
}

func deployReplicas(dep *appsv1.Deployment) int32 {
	if dep != nil && dep.Spec.Replicas != nil {
		return *dep.Spec.Replicas
	}
	return 1
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func (p *scalePlan) apply(ctx context.Context, c client.Client, policyName string) (bool, error) {
	scaled := false
	for _, s := range p.steps {
		current, err := backends.Current(ctx, c, s.target)
		if err != nil {
			return scaled, wrapBackendErr(s.target, err)
		}
		if current < s.desired {
			if err := backends.ScaleUp(ctx, c, s.target, s.desired); err != nil {
				return scaled, wrapBackendErr(s.target, err)
			}
			metrics.ScaleActions.WithLabelValues(policyName, "up", metricBackend(s), "ok").Inc()
			scaled = true
		}
		if err := backends.PatchAnnotations(ctx, c, s.target, s.stamp); err != nil {
			return scaled, wrapBackendErr(s.target, err)
		}
	}
	return scaled, nil
}

// wrapBackendErr turns API Forbidden into a clear missing-RBAC error for catalog targets.
func wrapBackendErr(t backends.Target, err error) error {
	if err == nil {
		return nil
	}
	if apierrors.IsForbidden(err) {
		return &backendAccessError{Target: t, Err: err}
	}
	return fmt.Errorf("%s %s: %w", t.Kind, t.ObjectKey, err)
}

type backendAccessError struct {
	Target backends.Target
	Err    error
}

func (e *backendAccessError) Error() string {
	return fmt.Sprintf(
		"missing RBAC for %s %q (apiVersion=%s); grant get/list/watch/patch via Helm extraClusterRoleRules: %v",
		e.Target.Kind, e.Target.ObjectKey, e.Target.APIVersion, e.Err,
	)
}

func (e *backendAccessError) Unwrap() error { return e.Err }

func isMissingBackendRBAC(err error) bool {
	var e *backendAccessError
	return errors.As(err, &e)
}

func metricBackend(s scaleStep) string {
	if s.key != "" {
		return s.key
	}
	return s.target.Kind
}

func (p *scalePlan) actions(existing []egv1a1.ScaleAction) []egv1a1.ScaleAction {
	var next []egv1a1.ScaleAction
	for _, s := range p.steps {
		next = append(next, scaleAction(s.key, s.target, s.baseline, s.desired, s.stampKeys))
	}
	if len(p.steps) == 0 {
		// External-only: no capacity patches; do not keep stale actions from an older catalog.
		return next
	}
	return mergeActions(existing, next)
}

func restoreActions(ctx context.Context, c client.Client, win *egv1a1.EvictionGuardWindow, deployFloor int32) error {
	// Preserve Spec.Actions order (workload binding order); do not re-sort by kind.
	actions := win.ScaleActions()
	coord := hasDeployAction(actions) && hasHPAMinAction(actions)
	for _, a := range actions {
		target := actionTarget(a)
		if target.Namespace == "" {
			target.Namespace = win.Namespace
		}
		current, err := backends.Current(ctx, c, target)
		if err != nil {
			return wrapBackendErr(target, err)
		}
		floor := a.Baseline
		if isDeployAction(a) {
			floor = deployFloor
		}
		if current > floor {
			switch {
			case isHPAMinAction(a) && coord:
				if err := backends.RestoreMinReplicas(ctx, c, target, floor); err != nil {
					return wrapBackendErr(target, err)
				}
			case isHPAMinAction(a):
				if err := backends.ScaleDownMinReplicas(ctx, c, target, floor); err != nil {
					return wrapBackendErr(target, err)
				}
			default:
				if err := backends.ScaleDown(ctx, c, target, floor); err != nil {
					return wrapBackendErr(target, err)
				}
			}
		}
		if err := backends.PatchAnnotations(ctx, c, target, backends.StampDeletes(a.StampKeys)); err != nil {
			return wrapBackendErr(target, err)
		}
	}
	return nil
}

func hasDeployAction(actions []egv1a1.ScaleAction) bool {
	for _, a := range actions {
		if isDeployAction(a) {
			return true
		}
	}
	return false
}

func hasHPAMinAction(actions []egv1a1.ScaleAction) bool {
	for _, a := range actions {
		if isHPAMinAction(a) {
			return true
		}
	}
	return false
}

func isDeployAction(a egv1a1.ScaleAction) bool {
	return a.Kind == "Deployment"
}

// isHPAMinAction is the HPA floor path that must not fight status.currentReplicas (G4).
// Other HPA fields (e.g. spec.maxReplicas) use the generic field patcher.
func isHPAMinAction(a egv1a1.ScaleAction) bool {
	if a.Kind != "HorizontalPodAutoscaler" {
		return false
	}
	return a.FieldPath == "" || a.FieldPath == "spec.minReplicas"
}

func workloadDeployment(ctx context.Context, c client.Client, ns, name string) (*appsv1.Deployment, error) {
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, dep); client.IgnoreNotFound(err) != nil {
		return nil, err
	}
	return dep, nil
}

func backendLabel(dep *appsv1.Deployment, policy *egv1a1.EvictionGuardPolicy) string {
	bindings, err := backends.BindingsForWorkload(dep, policy)
	if err != nil || len(bindings) == 0 {
		return "unknown"
	}
	parts := make([]string, len(bindings))
	for i, b := range bindings {
		parts[i] = b.Key
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
