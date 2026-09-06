//go:build envtest

/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package envtest

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/internal/controller"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

// TestScaleUpSpareReadyScaleBack is the v1alpha1 acceptance loop against a real API server:
// Karpenter taint → scale 3→4 → Ready spare → at-risk still present keeps Open →
// delete at-risk → scale back immediately → Cooling → close.
// (Eviction sequencing is enforced by the pods/eviction webhook, covered in pkg/evictgate.)
func TestScaleUpSpareReadyScaleBack(t *testing.T) {
	ns := "eg-loop"
	createNS(t, ns)
	ctx := context.Background()

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: ns,
			Annotations: map[string]string{
				egv1a1.ScaleBackendAnnotation: "deployment",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(3)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
					"app":                 "web",
					egv1a1.ProtectedLabel: "true",
				}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "web", Image: "nginx:1.27"}},
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, dep); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dep), dep); err != nil {
		t.Fatal(err)
	}

	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-rs",
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: dep.Name, UID: dep.UID, Controller: ptr.To(true),
			}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: ptr.To(int32(3)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: dep.Spec.Template,
		},
	}
	if err := k8sClient.Create(ctx, rs); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(rs), rs); err != nil {
		t.Fatal(err)
	}

	atRisk := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-on-dying",
			Namespace: ns,
			Labels: map[string]string{
				"app":                 "web",
				egv1a1.ProtectedLabel: "true",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: rs.Name, UID: rs.UID, Controller: ptr.To(true),
			}},
		},
		Spec: corev1.PodSpec{
			NodeName:   "worker-1",
			Containers: []corev1.Container{{Name: "web", Image: "nginx:1.27"}},
		},
	}
	if err := k8sClient.Create(ctx, atRisk); err != nil {
		t.Fatal(err)
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "worker-1",
			Labels: map[string]string{"karpenter.sh/capacity-type": "spot"},
		},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule}},
		},
	}
	if err := k8sClient.Create(ctx, node); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), node) })

	policy := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot-workers"},
		Spec: egv1a1.EvictionGuardPolicySpec{
			NodeFilter:     egv1a1.NodeFilter{CapacityTypes: []string{"spot"}},
			SpareReplicas:  ptr.To(int32(1)),
			MaxBuffer:      ptr.To(int32(4)),
			ScaleBackAfter: &metav1.Duration{Duration: 2 * time.Second},
			Backends: map[string]egv1a1.BackendCatalogEntry{
				"deployment": {
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Patches:    []egv1a1.BackendPatch{{Path: "spec.replicas"}},
				},
			},
		},
	}
	if err := k8sClient.Create(ctx, policy); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), policy) })

	winName := types.NamespacedName{Namespace: ns, Name: controller.WindowName(policy.Name, ns, dep.Name)}

	eventually(t, 15*time.Second, func(ctx context.Context) error {
		got := &appsv1.Deployment{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dep), got); err != nil {
			return err
		}
		if got.Spec.Replicas == nil || *got.Spec.Replicas != 4 {
			return fmt.Errorf("replicas=%v, want 4", got.Spec.Replicas)
		}
		win := &egv1a1.EvictionGuardWindow{}
		if err := k8sClient.Get(ctx, winName, win); err != nil {
			return err
		}
		if win.Status.Phase != egv1a1.WindowPhaseOpen {
			return fmt.Errorf("phase=%s, want Open", win.Status.Phase)
		}
		return nil
	})

	for i := 0; i < 3; i++ {
		p := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("web-safe-%d", i),
				Namespace: ns,
				Labels: map[string]string{
					"app":                 "web",
					egv1a1.ProtectedLabel: "true",
				},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1", Kind: "ReplicaSet", Name: rs.Name, UID: rs.UID, Controller: ptr.To(true),
				}},
			},
			Spec: corev1.PodSpec{
				NodeName:   "worker-2",
				Containers: []corev1.Container{{Name: "web", Image: "nginx:1.27"}},
			},
		}
		if err := k8sClient.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
		p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
		if err := k8sClient.Status().Update(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	eventually(t, 15*time.Second, func(ctx context.Context) error {
		win := &egv1a1.EvictionGuardWindow{}
		if err := k8sClient.Get(ctx, winName, win); err != nil {
			return err
		}
		if !win.Status.SpareReady {
			return fmt.Errorf("spareReady=false safe=%d", win.Status.SafeReadyReplicas)
		}
		if win.Status.Phase != egv1a1.WindowPhaseOpen {
			return fmt.Errorf("phase=%s, want Open while at-risk remains", win.Status.Phase)
		}
		if win.Spec.WindowUntil != nil {
			return fmt.Errorf("cooldown armed before at-risk gone: %v", win.Spec.WindowUntil)
		}
		got := &appsv1.Deployment{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dep), got); err != nil {
			return err
		}
		if got.Spec.Replicas == nil || *got.Spec.Replicas != 4 {
			return fmt.Errorf("replicas=%v, want 4 until at-risk gone", got.Spec.Replicas)
		}
		return nil
	})

	// Hold elevated capacity for a beat so a scale-back/scale-up flap fails the test.
	consistently(t, 2*time.Second, func(ctx context.Context) error {
		got := &appsv1.Deployment{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dep), got); err != nil {
			return err
		}
		if got.Spec.Replicas == nil || *got.Spec.Replicas != 4 {
			return fmt.Errorf("replicas flapped to %v while at-risk pod still present", got.Spec.Replicas)
		}
		win := &egv1a1.EvictionGuardWindow{}
		if err := k8sClient.Get(ctx, winName, win); err != nil {
			return err
		}
		if win.Status.Phase != egv1a1.WindowPhaseOpen {
			return fmt.Errorf("phase flapped to %s", win.Status.Phase)
		}
		if win.Spec.WindowUntil != nil {
			return fmt.Errorf("cooldown armed while at-risk remains: %v", win.Spec.WindowUntil)
		}
		return nil
	})

	if err := k8sClient.Delete(ctx, atRisk); err != nil {
		t.Fatal(err)
	}

	eventually(t, 15*time.Second, func(ctx context.Context) error {
		got := &appsv1.Deployment{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dep), got); err != nil {
			return err
		}
		if got.Spec.Replicas == nil || *got.Spec.Replicas != 3 {
			return fmt.Errorf("replicas=%v, want 3 immediately after at-risk gone", got.Spec.Replicas)
		}
		win := &egv1a1.EvictionGuardWindow{}
		if err := k8sClient.Get(ctx, winName, win); err != nil {
			return err
		}
		if win.Spec.WindowUntil == nil {
			return fmt.Errorf("windowUntil unset after scale-back")
		}
		if win.Status.Phase != egv1a1.WindowPhaseCooling {
			return fmt.Errorf("phase=%s, want Cooling", win.Status.Phase)
		}
		// Node may still carry the disruption taint; scale-back must not wait on it.
		n := &corev1.Node{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "worker-1"}, n); err != nil {
			return err
		}
		if len(n.Spec.Taints) == 0 {
			return fmt.Errorf("expected dying node still tainted to prove early scale-back")
		}
		return nil
	})

	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "worker-1"}, node); err != nil {
		t.Fatal(err)
	}
	node.Spec.Taints = nil
	if err := k8sClient.Update(ctx, node); err != nil {
		t.Fatal(err)
	}

	eventually(t, 20*time.Second, func(ctx context.Context) error {
		got := &appsv1.Deployment{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dep), got); err != nil {
			return err
		}
		if got.Spec.Replicas == nil || *got.Spec.Replicas != 3 {
			return fmt.Errorf("replicas=%v, want 3 after scale-back", got.Spec.Replicas)
		}
		win := &egv1a1.EvictionGuardWindow{}
		err := k8sClient.Get(ctx, winName, win)
		if client.IgnoreNotFound(err) != nil {
			return err
		}
		if err == nil {
			return fmt.Errorf("window still present phase=%s", win.Status.Phase)
		}
		return nil
	})
}
