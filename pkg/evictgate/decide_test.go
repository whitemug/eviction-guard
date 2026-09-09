/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package evictgate_test

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
	"github.com/whitemug/eviction-guard/pkg/evictgate"
	"github.com/whitemug/eviction-guard/pkg/naming"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

func scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := egv1a1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEvaluateAllowsNonOptedIn(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"}}
	c := fake.NewClientBuilder().WithScheme(scheme(t)).Build()
	res, err := evictgate.Evaluate(context.Background(), c, pod)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != evictgate.Allow {
		t.Fatalf("got %+v", res)
	}
}

func TestEvaluateDeniesUntilSpareReady(t *testing.T) {
	s := scheme(t)
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{"karpenter.sh/capacity-type": "spot"}},
		Spec:       corev1.NodeSpec{Taints: []corev1.Taint{{Key: signals.TaintKarpenterDisrupted}}},
	}
	policy := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot"},
		Spec:       egv1a1.EvictionGuardPolicySpec{NodeFilter: egv1a1.NodeFilter{CapacityTypes: []string{"spot"}}},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", egv1a1.ProtectedLabel: "true"}},
			},
		},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-rs", Namespace: "app",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "d1", Controller: ptr.To(true),
			}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-a", Namespace: "app",
			Labels: map[string]string{"app": "web", egv1a1.ProtectedLabel: "true"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-rs", UID: "r1", Controller: ptr.To(true),
			}},
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	win := &egv1a1.EvictionGuardWindow{
		ObjectMeta: metav1.ObjectMeta{Name: naming.WindowName("spot", "app", "web"), Namespace: "app"},
		Spec: egv1a1.EvictionGuardWindowSpec{
			PolicyName:      "spot",
			VulnerableNodes: []string{"n1"},
			Baseline:        3,
			Target:          egv1a1.WorkloadReference{Name: "web", Namespace: "app", Kind: "Deployment"},
		},
		Status: egv1a1.EvictionGuardWindowStatus{Phase: egv1a1.WindowPhaseOpen, SpareReady: false},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&egv1a1.EvictionGuardWindow{}).
		WithObjects(node, policy, dep, rs, pod, win).Build()
	res, err := evictgate.Evaluate(context.Background(), c, pod)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != evictgate.Deny {
		t.Fatalf("want deny, got %+v", res)
	}

	win.Status.SpareReady = true
	if err := c.Status().Update(context.Background(), win); err != nil {
		t.Fatal(err)
	}
	res, err = evictgate.Evaluate(context.Background(), c, pod)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != evictgate.Allow {
		t.Fatalf("want allow after SpareReady, got %+v", res)
	}
}

func TestEvaluateOneAtATime(t *testing.T) {
	s := scheme(t)
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{"karpenter.sh/capacity-type": "spot"}},
		Spec:       corev1.NodeSpec{Unschedulable: true},
	}
	policy := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot"},
		Spec:       egv1a1.EvictionGuardPolicySpec{NodeFilter: egv1a1.NodeFilter{CapacityTypes: []string{"spot"}}},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", egv1a1.ProtectedLabel: "true"}},
			},
		},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-rs", Namespace: "app",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "d1", Controller: ptr.To(true),
			}},
		},
	}
	podA := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-a", Namespace: "app",
			Labels: map[string]string{"app": "web", egv1a1.ProtectedLabel: "true"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-rs", UID: "r1", Controller: ptr.To(true),
			}},
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	podB := podA.DeepCopy()
	podB.Name = "web-b"
	win := &egv1a1.EvictionGuardWindow{
		ObjectMeta: metav1.ObjectMeta{Name: naming.WindowName("spot", "app", "web"), Namespace: "app"},
		Spec: egv1a1.EvictionGuardWindowSpec{
			PolicyName: "spot", VulnerableNodes: []string{"n1"}, Baseline: 2,
			Target: egv1a1.WorkloadReference{Name: "web", Namespace: "app", Kind: "Deployment"},
		},
		Status: egv1a1.EvictionGuardWindowStatus{Phase: egv1a1.WindowPhaseOpen, SpareReady: true},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(node, policy, dep, rs, podA, podB, win).Build()

	res, err := evictgate.Evaluate(context.Background(), c, podB)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != evictgate.Deny {
		t.Fatalf("web-b should wait, got %+v", res)
	}
	res, err = evictgate.Evaluate(context.Background(), c, podA)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != evictgate.Allow {
		t.Fatalf("web-a should proceed, got %+v", res)
	}
}

func TestEvaluateForceCoolAllows(t *testing.T) {
	s := scheme(t)
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{"karpenter.sh/capacity-type": "spot"}},
		Spec:       corev1.NodeSpec{Taints: []corev1.Taint{{Key: signals.TaintKarpenterDisrupted}}},
	}
	policy := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot"},
		Spec:       egv1a1.EvictionGuardPolicySpec{NodeFilter: egv1a1.NodeFilter{CapacityTypes: []string{"spot"}}},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", egv1a1.ProtectedLabel: "true"}},
			},
		},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-rs", Namespace: "app",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "d1", Controller: ptr.To(true),
			}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-a", Namespace: "app",
			Labels: map[string]string{"app": "web", egv1a1.ProtectedLabel: "true"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-rs", UID: "r1", Controller: ptr.To(true),
			}},
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	win := &egv1a1.EvictionGuardWindow{
		ObjectMeta: metav1.ObjectMeta{Name: naming.WindowName("spot", "app", "web"), Namespace: "app"},
		Spec: egv1a1.EvictionGuardWindowSpec{
			PolicyName: "spot", VulnerableNodes: []string{"n1"}, Baseline: 3,
			Target: egv1a1.WorkloadReference{Name: "web", Namespace: "app", Kind: "Deployment"},
		},
		Status: egv1a1.EvictionGuardWindowStatus{Phase: egv1a1.WindowPhaseCooling, SpareReady: false, ForcedCool: true},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(node, policy, dep, rs, pod, win).Build()
	res, err := evictgate.Evaluate(context.Background(), c, pod)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != evictgate.Allow {
		t.Fatalf("ForcedCool should allow, got %+v", res)
	}
}

func TestEvaluateAllowsNonVulnerableWithActiveWindow(t *testing.T) {
	// Sibling spot disruption must not gate pods on healthy nodes.
	s := scheme(t)
	spot := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "spot-1", Labels: map[string]string{"karpenter.sh/capacity-type": "spot"}},
		Spec:       corev1.NodeSpec{Taints: []corev1.Taint{{Key: signals.TaintKarpenterDisrupted}}},
	}
	ondemand := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "od-1", Labels: map[string]string{"karpenter.sh/capacity-type": "on-demand"}},
	}
	policy := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot"},
		Spec:       egv1a1.EvictionGuardPolicySpec{NodeFilter: egv1a1.NodeFilter{CapacityTypes: []string{"spot"}}},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", egv1a1.ProtectedLabel: "true"}},
			},
		},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-rs", Namespace: "app",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "d1", Controller: ptr.To(true),
			}},
		},
	}
	safePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-safe", Namespace: "app",
			Labels: map[string]string{"app": "web", egv1a1.ProtectedLabel: "true"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-rs", UID: "r1", Controller: ptr.To(true),
			}},
		},
		Spec: corev1.PodSpec{NodeName: "od-1"},
	}
	win := &egv1a1.EvictionGuardWindow{
		ObjectMeta: metav1.ObjectMeta{Name: naming.WindowName("spot", "app", "web"), Namespace: "app"},
		Spec: egv1a1.EvictionGuardWindowSpec{
			PolicyName: "spot", VulnerableNodes: []string{"spot-1"}, Baseline: 3,
			Target: egv1a1.WorkloadReference{Name: "web", Namespace: "app", Kind: "Deployment"},
		},
		Status: egv1a1.EvictionGuardWindowStatus{Phase: egv1a1.WindowPhaseOpen, SpareReady: false},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(spot, ondemand, policy, dep, rs, safePod, win).Build()
	res, err := evictgate.Evaluate(context.Background(), c, safePod)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != evictgate.Allow {
		t.Fatalf("non-vulnerable pod must allow while window open, got %+v", res)
	}
}

func TestEvaluateDeniesWhenDeferredNoWindow(t *testing.T) {
	// Vulnerable node + opted-in pod but no window yet (e.g. maxConcurrentWindows) → deny.
	s := scheme(t)
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{"karpenter.sh/capacity-type": "spot"}},
		Spec:       corev1.NodeSpec{Unschedulable: true},
	}
	policy := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot"},
		Spec:       egv1a1.EvictionGuardPolicySpec{NodeFilter: egv1a1.NodeFilter{CapacityTypes: []string{"spot"}}},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", egv1a1.ProtectedLabel: "true"}},
			},
		},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-rs", Namespace: "app",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "d1", Controller: ptr.To(true),
			}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-a", Namespace: "app",
			Labels: map[string]string{"app": "web", egv1a1.ProtectedLabel: "true"},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-rs", UID: "r1", Controller: ptr.To(true),
			}},
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(node, policy, dep, rs, pod).Build()
	res, err := evictgate.Evaluate(context.Background(), c, pod)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != evictgate.Deny {
		t.Fatalf("deferred/no-window must deny, got %+v", res)
	}
}
