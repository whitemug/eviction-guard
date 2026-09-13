/*
Copyright 2026 Whitemug.

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
	key             string
	target          backends.Target
	baseline        int32
	desired         int32
	stamp           map[string]string
	stampKeys       []string
	skipDownscaling bool
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
			key:             step.Key,
			target:          step.Target,
			baseline:        baseline,
			desired:         plan.desired,
			stamp:           anns,
			stampKeys:       uniqueStrings(keys),
			skipDownscaling: step.SkipDownscaling,
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
	// Walk steps top-to-bottom. Consecutive paths on the same object are one
	// merge patch (list order preserved; no reordering).
	for i := 0; i < len(p.steps); {
		group := []scaleStep{p.steps[i]}
		j := i + 1
		for j < len(p.steps) && sameScaleObject(p.steps[i].target, p.steps[j].target) {
			group = append(group, p.steps[j])
			j++
		}
		var paths []string
		values := map[string]int32{}
		var stamp map[string]string
		raised := false
		for _, s := range group {
			current, err := backends.Current(ctx, c, s.target)
			if err != nil {
				return scaled, wrapBackendErr(s.target, err)
			}
			path := s.target.FieldPath
			paths = append(paths, path)
			if current < s.desired {
				values[path] = s.desired
				raised = true
			}
			if len(s.stamp) > 0 {
				if stamp == nil {
					stamp = map[string]string{}
				}
				for k, v := range s.stamp {
					stamp[k] = v
				}
			}
		}
		if len(values) > 0 {
			base := group[0].target
			if err := backends.PatchIntegers(ctx, c, base, paths, values); err != nil {
				return scaled, wrapBackendErr(base, err)
			}
			if raised {
				metrics.ScaleActions.WithLabelValues(policyName, "up", metricBackend(group[0]), "ok").Inc()
				scaled = true
			}
		}
		if len(stamp) > 0 {
			if err := backends.PatchAnnotations(ctx, c, group[0].target, stamp); err != nil {
				return scaled, wrapBackendErr(group[0].target, err)
			}
		}
		i = j
	}
	return scaled, nil
}

func sameScaleObject(a, b backends.Target) bool {
	return a.APIVersion == b.APIVersion && a.Kind == b.Kind && a.Namespace == b.Namespace && a.Name == b.Name
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
		next = append(next, scaleAction(s.key, s.target, s.baseline, s.desired, s.stampKeys, s.skipDownscaling))
	}
	if len(p.steps) == 0 {
		// External-only: no capacity patches; do not keep stale actions from an older catalog.
		return next
	}
	return mergeActions(existing, next)
}

func restoreActions(ctx context.Context, c client.Client, win *egv1a1.EvictionGuardWindow) error {
	// Top-to-bottom Spec.Actions order. Consecutive paths on the same object
	// restore in one merge patch (no reordering). Each action reverts to its
	// own baseline unless SkipDownscaling is set (up-only).
	actions := win.ScaleActions()
	for i := 0; i < len(actions); {
		a := actions[i]
		if a.Namespace == "" {
			a.Namespace = win.Namespace
		}
		target := actionTarget(a)
		group := []egv1a1.ScaleAction{a}
		j := i + 1
		for j < len(actions) {
			aj := actions[j]
			if aj.Namespace == "" {
				aj.Namespace = win.Namespace
			}
			if !sameScaleObject(target, actionTarget(aj)) {
				break
			}
			group = append(group, aj)
			j++
		}

		if len(group) == 1 {
			if err := restoreOneAction(ctx, c, group[0]); err != nil {
				if apierrors.IsNotFound(err) {
					i = j
					continue
				}
				return err
			}
		} else {
			if err := restoreGroupedActions(ctx, c, group); err != nil {
				if apierrors.IsNotFound(err) {
					i = j
					continue
				}
				return err
			}
		}
		i = j
	}
	return nil
}

// restoreGroupedActions restores consecutive paths on one object as a single merge patch.
func restoreGroupedActions(ctx context.Context, c client.Client, group []egv1a1.ScaleAction) error {
	target := actionTarget(group[0])
	var paths []string
	values := map[string]int32{}
	var stampKeys []string
	for _, ga := range group {
		gt := actionTarget(ga)
		stampKeys = append(stampKeys, ga.StampKeys...)
		if ga.SkipDownscaling {
			continue
		}
		current, err := backends.Current(ctx, c, gt)
		if err != nil {
			return wrapBackendErr(gt, err)
		}
		paths = append(paths, gt.FieldPath)
		if current > ga.Baseline {
			values[gt.FieldPath] = ga.Baseline
		}
	}
	if len(values) > 0 {
		if err := backends.PatchIntegers(ctx, c, target, paths, values); err != nil {
			return wrapBackendErr(target, err)
		}
	}
	if err := backends.PatchAnnotations(ctx, c, target, backends.StampDeletes(uniqueStrings(stampKeys))); err != nil {
		return wrapBackendErr(target, err)
	}
	return nil
}

func restoreOneAction(ctx context.Context, c client.Client, a egv1a1.ScaleAction) error {
	target := actionTarget(a)
	if !a.SkipDownscaling {
		current, err := backends.Current(ctx, c, target)
		if err != nil {
			return wrapBackendErr(target, err)
		}
		if current > a.Baseline {
			if err := backends.ScaleDown(ctx, c, target, a.Baseline); err != nil {
				return wrapBackendErr(target, err)
			}
		}
	}
	if err := backends.PatchAnnotations(ctx, c, target, backends.StampDeletes(a.StampKeys)); err != nil {
		return wrapBackendErr(target, err)
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
