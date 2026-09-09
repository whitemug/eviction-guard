/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

func TestLiveAtRiskPodsScopesToWorkloadNodes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = egv1a1.AddToScheme(scheme)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(2)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
		},
	}
	atRisk := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-a",
			Namespace: "app",
			Labels:    map[string]string{"app": "web"},
		},
		Spec: corev1.PodSpec{NodeName: "spot-1"},
	}
	safe := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-b",
			Namespace: "app",
			Labels:    map[string]string{"app": "web"},
		},
		Spec: corev1.PodSpec{NodeName: "od-1"},
	}
	spot := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "spot-1",
			Labels: map[string]string{"karpenter.sh/capacity-type": "spot"},
		},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule}},
		},
	}
	od := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "od-1",
			Labels: map[string]string{"karpenter.sh/capacity-type": "on-demand"},
		},
	}
	// Extra vulnerable node with no workload pods — must not require listing the whole cluster
	// and must not affect this workload's at-risk count.
	other := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "spot-other",
			Labels: map[string]string{"karpenter.sh/capacity-type": "spot"},
		},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dep, atRisk, safe, spot, od, other).Build()
	policy := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot-workers"},
		Spec:       egv1a1.EvictionGuardPolicySpec{NodeFilter: egv1a1.NodeFilter{CapacityTypes: []string{"spot"}}},
	}
	win := &egv1a1.EvictionGuardWindow{
		Spec: egv1a1.EvictionGuardWindowSpec{
			Target: egv1a1.WorkloadReference{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "app", Name: "web"},
		},
	}
	n, dying, err := liveAtRiskPods(context.Background(), c, win, policy)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("atRisk=%d, want 1", n)
	}
	if len(dying) != 1 || dying[0] != "spot-1" {
		t.Fatalf("dying=%v, want [spot-1]", dying)
	}
}
