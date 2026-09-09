/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/filters"
	"github.com/whitemug/eviction-guard/pkg/metrics"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

func listWorkloadPods(ctx context.Context, c client.Reader, dep *appsv1.Deployment) ([]corev1.Pod, error) {
	if dep == nil {
		return nil, nil
	}
	sel, err := metav1.LabelSelectorAsSelector(dep.Spec.Selector)
	if err != nil {
		return nil, err
	}
	list := &corev1.PodList{}
	if err := c.List(ctx, list, client.InNamespace(dep.Namespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// spareCounts returns Ready pods and Ready pods not sitting on vulnerable nodes.
func (r *WindowReconciler) spareCounts(ctx context.Context, win *egv1a1.EvictionGuardWindow) (ready, safe int32, err error) {
	if k := win.Spec.Target.Kind; k != "" && k != "Deployment" {
		return 0, 0, nil
	}
	dep := &appsv1.Deployment{}
	key := types.NamespacedName{Namespace: win.Spec.Target.Namespace, Name: win.Spec.Target.Name}
	if err := r.Get(ctx, key, dep); err != nil {
		return 0, 0, client.IgnoreNotFound(err)
	}
	sel, err := metav1.LabelSelectorAsSelector(dep.Spec.Selector)
	if err != nil {
		return 0, 0, err
	}
	list := &corev1.PodList{}
	if err := r.List(ctx, list, client.InNamespace(dep.Namespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return 0, 0, err
	}
	vuln := map[string]struct{}{}
	for _, n := range win.Spec.VulnerableNodes {
		vuln[n] = struct{}{}
	}
	for i := range list.Items {
		p := &list.Items[i]
		if !podReady(p) {
			continue
		}
		ready++
		if _, dying := vuln[p.Spec.NodeName]; !dying {
			safe++
		}
	}
	return ready, safe, nil
}

// liveAtRiskPods counts non-terminating target pods on nodes that currently
// match the policy filter and carry a disruption signal. It does not depend on
// Spec.VulnerableNodes.
//
// Cost is O(workload pods + unique node Gets), not a cluster-wide Node list:
// only nodes that already host this workload can contribute at-risk pods.
func liveAtRiskPods(ctx context.Context, c client.Reader, win *egv1a1.EvictionGuardWindow, policy *egv1a1.EvictionGuardPolicy) (int, []string, error) {
	if win == nil || policy == nil {
		return 0, nil, nil
	}
	if k := win.Spec.Target.Kind; k != "" && k != "Deployment" {
		return 0, nil, nil
	}
	filter, err := filters.FromSpec(policy.Spec.NodeFilter)
	if err != nil {
		return 0, nil, err
	}
	dep := &appsv1.Deployment{}
	key := types.NamespacedName{Namespace: win.Spec.Target.Namespace, Name: win.Spec.Target.Name}
	if err := c.Get(ctx, key, dep); err != nil {
		return 0, nil, client.IgnoreNotFound(err)
	}
	pods, err := listWorkloadPods(ctx, c, dep)
	if err != nil {
		return 0, nil, err
	}

	nodeCache := map[string]*corev1.Node{}
	dying := map[string]struct{}{}
	atRisk := 0
	for i := range pods {
		p := &pods[i]
		if p.DeletionTimestamp != nil || p.Spec.NodeName == "" {
			continue
		}
		vulnerable, err := nodeVulnerableCached(ctx, c, filter, policy, p.Spec.NodeName, nodeCache)
		if err != nil {
			return 0, nil, err
		}
		if !vulnerable {
			continue
		}
		dying[p.Spec.NodeName] = struct{}{}
		atRisk++
	}
	dyingNames := make([]string, 0, len(dying))
	for name := range dying {
		dyingNames = append(dyingNames, name)
	}
	sort.Strings(dyingNames)
	return atRisk, dyingNames, nil
}

func nodeVulnerableCached(ctx context.Context, c client.Reader, filter filters.Filter, policy *egv1a1.EvictionGuardPolicy, name string, cache map[string]*corev1.Node) (bool, error) {
	n, ok := cache[name]
	if !ok {
		n = &corev1.Node{}
		if err := c.Get(ctx, types.NamespacedName{Name: name}, n); err != nil {
			if client.IgnoreNotFound(err) == nil {
				cache[name] = nil
				return false, nil
			}
			return false, err
		}
		cache[name] = n
	}
	if n == nil {
		return false, nil
	}
	ok, err := filter.Matches(n)
	if err != nil || !ok {
		return false, err
	}
	return signals.Vulnerable(n, policy), nil
}

func podReady(p *corev1.Pod) bool {
	if p.DeletionTimestamp != nil {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func applySpareStatus(win *egv1a1.EvictionGuardWindow, ready, safe int32, now metav1.Time) (transitioned bool) {
	win.Status.ReadyReplicas = ready
	win.Status.SafeReadyReplicas = safe
	want := safe >= win.Spec.Baseline
	prev := meta.FindStatusCondition(win.Status.Conditions, egv1a1.ConditionSpareReady)
	transitioned = prev == nil || (prev.Status == metav1.ConditionTrue) != want

	cond := metav1.Condition{
		Type:               egv1a1.ConditionSpareReady,
		ObservedGeneration: win.Generation,
		LastTransitionTime: now,
	}
	if want {
		win.Status.SpareReady = true
		cond.Status = metav1.ConditionTrue
		cond.Reason = egv1a1.ReasonReplicasReady
		cond.Message = fmt.Sprintf("safeReady=%d baseline=%d ready=%d", safe, win.Spec.Baseline, ready)
		metrics.SpareNotReady.WithLabelValues(win.Spec.PolicyName, win.Spec.Target.Namespace, win.Spec.Target.Name).Set(0)
	} else {
		win.Status.SpareReady = false
		cond.Status = metav1.ConditionFalse
		cond.Reason = egv1a1.ReasonWaitingForReady
		cond.Message = fmt.Sprintf("safeReady=%d < baseline=%d (ready=%d); extra pods not Ready off vulnerable nodes", safe, win.Spec.Baseline, ready)
		metrics.SpareNotReady.WithLabelValues(win.Spec.PolicyName, win.Spec.Target.Namespace, win.Spec.Target.Name).Set(1)
	}
	meta.SetStatusCondition(&win.Status.Conditions, cond)
	return transitioned
}

func (r *WindowReconciler) workloadToWindows(ctx context.Context, obj client.Object) []reconcile.Request {
	list := &egv1a1.EvictionGuardWindowList{}
	if err := r.List(ctx, list, client.InNamespace(obj.GetNamespace()), client.MatchingLabels{
		egv1a1.WorkloadNameLabel: obj.GetName(),
	}); err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		w := &list.Items[i]
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: w.Namespace, Name: w.Name}})
	}
	return reqs
}

func (r *WindowReconciler) podToWindows(ctx context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	var deployName string
	for _, o := range pod.OwnerReferences {
		if o.Kind != "ReplicaSet" || o.Controller == nil || !*o.Controller {
			continue
		}
		rs := &appsv1.ReplicaSet{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: o.Name}, rs); err != nil {
			return nil
		}
		for _, oo := range rs.OwnerReferences {
			if oo.Kind == "Deployment" && oo.Controller != nil && *oo.Controller {
				deployName = oo.Name
				break
			}
		}
	}
	if deployName == "" {
		return nil
	}
	list := &egv1a1.EvictionGuardWindowList{}
	if err := r.List(ctx, list, client.InNamespace(pod.Namespace), client.MatchingLabels{
		egv1a1.WorkloadNameLabel: deployName,
	}); err != nil {
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		w := &list.Items[i]
		reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: w.Namespace, Name: w.Name}})
	}
	return reqs
}
