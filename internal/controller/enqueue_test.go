/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

func enqueueScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := egv1a1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPoliciesForNodeRespectsFilterAndVulnerability(t *testing.T) {
	spotPol := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot-workers"},
		Spec: egv1a1.EvictionGuardPolicySpec{
			NodeFilter: egv1a1.NodeFilter{CapacityTypes: []string{"spot"}},
		},
	}
	odPol := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "on-demand"},
		Spec: egv1a1.EvictionGuardPolicySpec{
			NodeFilter: egv1a1.NodeFilter{CapacityTypes: []string{"on-demand"}},
		},
	}
	spotNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "spot-1",
			Labels: map[string]string{"karpenter.sh/capacity-type": "spot"},
		},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule}},
		},
	}
	healthySpot := spotNode.DeepCopy()
	healthySpot.Name = "spot-healthy"
	healthySpot.Spec.Taints = nil

	c := fake.NewClientBuilder().WithScheme(enqueueScheme(t)).WithObjects(spotPol, odPol, spotNode, healthySpot).Build()
	ctx := context.Background()

	got := policyNames(policiesForNode(ctx, c, spotNode, true))
	if len(got) != 1 || got[0] != "spot-workers" {
		t.Fatalf("vulnerable spot node: %v, want [spot-workers]", got)
	}
	if got := policyNames(policiesForNode(ctx, c, healthySpot, true)); len(got) != 0 {
		t.Fatalf("healthy spot node should enqueue nothing when onlyVulnerable, got %v", got)
	}
	got = policyNames(policiesForNode(ctx, c, healthySpot, false))
	if len(got) != 1 || got[0] != "spot-workers" {
		t.Fatalf("healthy spot without vulnerability filter: %v", got)
	}
}

func TestMapPodToPolicies(t *testing.T) {
	spotPol := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot-workers"},
		Spec:       egv1a1.EvictionGuardPolicySpec{NodeFilter: egv1a1.NodeFilter{CapacityTypes: []string{"spot"}}},
	}
	gpuPol := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-pool"},
		Spec: egv1a1.EvictionGuardPolicySpec{
			NodeFilter: egv1a1.NodeFilter{InstanceTypes: []string{"g5.xlarge"}},
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "spot-1",
			Labels: map[string]string{"karpenter.sh/capacity-type": "spot", "node.kubernetes.io/instance-type": "m6i.large"},
		},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(enqueueScheme(t)).WithObjects(spotPol, gpuPol, node).Build()
	r := &PolicyReconciler{Client: c}

	pending := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "app"}}
	if got := r.mapPodToPolicies(context.Background(), pending); len(got) != 0 {
		t.Fatalf("unscheduled pod: %v", got)
	}
	onSpot := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "app"},
		Spec:       corev1.PodSpec{NodeName: "spot-1"},
	}
	got := policyNames(r.mapPodToPolicies(context.Background(), onSpot))
	if len(got) != 1 || got[0] != "spot-workers" {
		t.Fatalf("pod on vulnerable spot: %v, want [spot-workers]", got)
	}
	missing := onSpot.DeepCopy()
	missing.Spec.NodeName = "gone"
	if got := r.mapPodToPolicies(context.Background(), missing); len(got) != 0 {
		t.Fatalf("unknown node: %v", got)
	}
}

func TestPoliciesForNodeUpdateUnionsOldAndNew(t *testing.T) {
	spotPol := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot-workers"},
		Spec:       egv1a1.EvictionGuardPolicySpec{NodeFilter: egv1a1.NodeFilter{CapacityTypes: []string{"spot"}}},
	}
	odPol := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "on-demand"},
		Spec:       egv1a1.EvictionGuardPolicySpec{NodeFilter: egv1a1.NodeFilter{CapacityTypes: []string{"on-demand"}}},
	}
	oldN := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "n1", Labels: map[string]string{"karpenter.sh/capacity-type": "spot"},
	}}
	newN := oldN.DeepCopy()
	newN.Labels["karpenter.sh/capacity-type"] = "on-demand"
	c := fake.NewClientBuilder().WithScheme(enqueueScheme(t)).WithObjects(spotPol, odPol, oldN).Build()
	got := policyNames(policiesForNodeUpdate(context.Background(), c, oldN, newN))
	have := map[string]bool{}
	for _, n := range got {
		have[n] = true
	}
	if !have["spot-workers"] || !have["on-demand"] || len(got) != 2 {
		t.Fatalf("label change spot→on-demand: %v, want both policies", got)
	}
	taintOnly := oldN.DeepCopy()
	taintOnly.Spec.Taints = []corev1.Taint{{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule}}
	got = policyNames(policiesForNodeUpdate(context.Background(), c, oldN, taintOnly))
	if len(got) != 1 || got[0] != "spot-workers" {
		t.Fatalf("taint on spot node: %v, want [spot-workers]", got)
	}
}

func policyNames(reqs []reconcile.Request) []string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.Name
	}
	return out
}

func TestPodMembershipChanged(t *testing.T) {
	base := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "app", Labels: map[string]string{"app": "web"}},
		Spec:       corev1.PodSpec{NodeName: "spot-1"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	ready := base.DeepCopy()
	ready.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	if podMembershipChanged(base, ready) {
		t.Fatal("Ready/status must not count as membership")
	}
	bound := base.DeepCopy()
	bound.Spec.NodeName = "spot-2"
	if !podMembershipChanged(base, bound) {
		t.Fatal("nodeName change must count")
	}
	labeled := base.DeepCopy()
	labeled.Labels[egv1a1.ProtectedLabel] = "true"
	if !podMembershipChanged(base, labeled) {
		t.Fatal("label change must count")
	}
	deleting := base.DeepCopy()
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	if !podMembershipChanged(base, deleting) {
		t.Fatal("deletionTimestamp must count")
	}
}

func TestPodPolicyPredicateSkipsReady(t *testing.T) {
	pred := podPolicyPredicate()
	oldP := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "app"},
		Spec:       corev1.PodSpec{NodeName: "spot-1"},
	}
	newP := oldP.DeepCopy()
	newP.Status.Phase = corev1.PodRunning
	if pred.Update(event.UpdateEvent{ObjectOld: oldP, ObjectNew: newP}) {
		t.Fatal("status-only update should be ignored")
	}
	if !pred.Create(event.CreateEvent{Object: oldP}) {
		t.Fatal("create should pass")
	}
	if !pred.Delete(event.DeleteEvent{Object: oldP}) {
		t.Fatal("delete should pass")
	}
}
