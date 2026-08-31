/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/metrics"
)

// spareCounts returns Ready pods and Ready pods not sitting on vulnerable nodes.
func (r *WindowReconciler) spareCounts(ctx context.Context, win *egv1a1.EvictionGuardWindow) (ready, safe int32, err error) {
	if win.Spec.Target.Kind != "" && win.Spec.Target.Kind != "Deployment" {
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
