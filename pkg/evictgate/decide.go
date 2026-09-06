/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package evictgate decides whether a voluntary Eviction of an opted-in pod
// may proceed. The validating webhook is the hard gate; controllers only
// scale and track EvictionGuardWindow state.
package evictgate

import (
	"context"
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/filters"
	"github.com/whitemug/eviction-guard/pkg/naming"
	"github.com/whitemug/eviction-guard/pkg/policyown"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

// Decision is the eviction admission outcome.
type Decision int

const (
	Allow Decision = iota
	Deny
)

// Result is the gate outcome plus a short reason for admission messages.
type Result struct {
	Decision Decision
	Reason   string
}

// Evaluate decides whether an Eviction of pod may proceed.
//
// Rules:
//   - Non-opted-in pods: allow
//   - No owning policy (pin missing / no selector match): allow
//   - Owning policy's nodeFilter does not match and no active window: allow
//   - ForcedCool window: allow (fail-open after maxWindow)
//   - SpareReady: allow only the lexicographically first at-risk pod (one at a time)
//   - Otherwise: deny (controller must open/scale the window)
func Evaluate(ctx context.Context, c client.Client, pod *corev1.Pod) (Result, error) {
	if pod == nil {
		return Result{Allow, ""}, nil
	}
	if !optedIn(pod) {
		return Result{Allow, "not opted in"}, nil
	}
	if pod.Spec.NodeName == "" {
		return Result{Allow, "unscheduled"}, nil
	}

	dep, err := ownerDeployment(ctx, c, pod)
	if err != nil {
		return Result{}, err
	}
	if dep == nil || !templateOptedIn(dep) {
		return Result{Allow, "workload not opted in"}, nil
	}

	node := &corev1.Node{}
	if err := c.Get(ctx, types.NamespacedName{Name: pod.Spec.NodeName}, node); err != nil {
		if apierrors.IsNotFound(err) {
			return Result{Allow, "node gone"}, nil
		}
		return Result{}, err
	}

	policies, err := listPolicies(ctx, c)
	if err != nil {
		return Result{}, err
	}
	nsLabels, err := namespaceLabelSet(ctx, c, pod.Namespace)
	if err != nil {
		return Result{}, err
	}
	owner := policyown.ResolveOwner(dep, nsLabels, policies)
	if owner == nil {
		return Result{Allow, "no matching policy"}, nil
	}

	filter, err := filters.FromSpec(owner.Spec.NodeFilter)
	if err != nil {
		return Result{}, err
	}
	nodeOK, err := filter.Matches(node)
	if err != nil {
		return Result{}, err
	}

	vulnerable := nodeOK && signals.Vulnerable(node, owner)

	win, err := activeWindowFor(ctx, c, dep, owner)
	if err != nil {
		return Result{}, err
	}

	if !vulnerable && win == nil {
		return Result{Allow, "no disruption"}, nil
	}
	if win != nil && win.Status.ForcedCool {
		return Result{Allow, "maxWindow force-cool"}, nil
	}
	if win != nil && win.Status.Phase == egv1a1.WindowPhaseClosed {
		return Result{Allow, "window closed"}, nil
	}

	if win == nil || !win.Status.SpareReady {
		why := "waiting for eviction-guard spare capacity"
		if win == nil {
			why = "waiting for eviction-guard window and spare capacity"
		}
		return Result{Deny, why}, nil
	}

	vulnNodes := win.Spec.VulnerableNodes
	if len(vulnNodes) == 0 {
		vulnNodes = []string{pod.Spec.NodeName}
	}
	next, err := nextAtRiskPod(ctx, c, dep, vulnNodes)
	if err != nil {
		return Result{}, err
	}
	if next == "" {
		return Result{Allow, "no at-risk pods"}, nil
	}
	if pod.Name != next {
		return Result{Deny, fmt.Sprintf("waiting for eviction of %s first", next)}, nil
	}
	return Result{Allow, "spare ready; next at-risk pod"}, nil
}

func optedIn(pod *corev1.Pod) bool {
	return pod.Labels != nil && pod.Labels[egv1a1.ProtectedLabel] == "true"
}

func templateOptedIn(dep *appsv1.Deployment) bool {
	return dep.Spec.Template.Labels != nil && dep.Spec.Template.Labels[egv1a1.ProtectedLabel] == "true"
}

func listPolicies(ctx context.Context, c client.Client) ([]*egv1a1.EvictionGuardPolicy, error) {
	list := &egv1a1.EvictionGuardPolicyList{}
	if err := c.List(ctx, list); err != nil {
		return nil, err
	}
	out := make([]*egv1a1.EvictionGuardPolicy, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, &list.Items[i])
	}
	return out, nil
}

func namespaceLabelSet(ctx context.Context, c client.Client, nsName string) (labels.Set, error) {
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: nsName}, ns); err != nil {
		if apierrors.IsNotFound(err) {
			return labels.Set{}, nil
		}
		return nil, err
	}
	return labels.Set(ns.Labels), nil
}

func activeWindowFor(ctx context.Context, c client.Client, dep *appsv1.Deployment, policy *egv1a1.EvictionGuardPolicy) (*egv1a1.EvictionGuardWindow, error) {
	if policy == nil {
		return nil, nil
	}
	win := &egv1a1.EvictionGuardWindow{}
	err := c.Get(ctx, types.NamespacedName{
		Namespace: dep.Namespace,
		Name:      naming.WindowName(policy.Name, dep.Namespace, dep.Name),
	}, win)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if win.IsActive() || win.Status.ForcedCool {
		return win, nil
	}
	return nil, nil
}

func ownerDeployment(ctx context.Context, c client.Client, pod *corev1.Pod) (*appsv1.Deployment, error) {
	var rsName string
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "ReplicaSet" && ref.Controller != nil && *ref.Controller {
			rsName = ref.Name
			break
		}
	}
	if rsName == "" {
		return nil, nil
	}
	rs := &appsv1.ReplicaSet{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: rsName}, rs); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	for _, ref := range rs.OwnerReferences {
		if ref.Kind == "Deployment" && ref.Controller != nil && *ref.Controller {
			dep := &appsv1.Deployment{}
			if err := c.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: ref.Name}, dep); err != nil {
				return nil, client.IgnoreNotFound(err)
			}
			return dep, nil
		}
	}
	return nil, nil
}

func nextAtRiskPod(ctx context.Context, c client.Client, dep *appsv1.Deployment, vulnNodes []string) (string, error) {
	vuln := map[string]struct{}{}
	for _, n := range vulnNodes {
		vuln[n] = struct{}{}
	}
	sel, err := metav1.LabelSelectorAsSelector(dep.Spec.Selector)
	if err != nil {
		return "", err
	}
	list := &corev1.PodList{}
	if err := c.List(ctx, list, client.InNamespace(dep.Namespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return "", err
	}
	var names []string
	for i := range list.Items {
		p := &list.Items[i]
		if p.DeletionTimestamp != nil {
			continue
		}
		if p.Labels[egv1a1.ProtectedLabel] != "true" {
			continue
		}
		if _, ok := vuln[p.Spec.NodeName]; !ok {
			continue
		}
		names = append(names, p.Name)
	}
	if len(names) == 0 {
		return "", nil
	}
	sort.Strings(names)
	return names[0], nil
}
