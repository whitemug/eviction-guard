//go:build envtest

/*
Copyright 2026 Whitemug.

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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/internal/controller"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

var widgetGVK = schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

// TestCatalogWidgetScaleAndRestore patches a third-party CR via policy.spec.backends
// (unstructured merge-patch) and restores it on scale-back.
func TestCatalogWidgetScaleAndRestore(t *testing.T) {
	ns := "eg-widget"
	createNS(t, ns)
	ctx := context.Background()

	widget := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata": map[string]interface{}{
			"name":      "web",
			"namespace": ns,
		},
		"spec": map[string]interface{}{
			"replicas": int64(2),
		},
	}}
	widget.SetGroupVersionKind(widgetGVK)
	if err := k8sClient.Create(ctx, widget); err != nil {
		t.Fatal(err)
	}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: ns,
			Annotations: map[string]string{
				egv1a1.ScaleBackendAnnotation: "deployment,widget",
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
			NodeName:   "widget-worker-1",
			Containers: []corev1.Container{{Name: "web", Image: "nginx:1.27"}},
		},
	}
	if err := k8sClient.Create(ctx, atRisk); err != nil {
		t.Fatal(err)
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "widget-worker-1",
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
		ObjectMeta: metav1.ObjectMeta{Name: "catalog-widget"},
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
				"widget": {
					APIVersion: "example.com/v1",
					Kind:       "Widget",
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

	eventually(t, 20*time.Second, func(ctx context.Context) error {
		got := &appsv1.Deployment{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dep), got); err != nil {
			return err
		}
		if got.Spec.Replicas == nil || *got.Spec.Replicas != 4 {
			return fmt.Errorf("deployment replicas=%v, want 4", got.Spec.Replicas)
		}
		w := &unstructured.Unstructured{}
		w.SetGroupVersionKind(widgetGVK)
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "web"}, w); err != nil {
			return err
		}
		replicas, ok, err := unstructured.NestedInt64(w.Object, "spec", "replicas")
		if err != nil || !ok || replicas != 4 {
			return fmt.Errorf("widget replicas=%v ok=%v err=%v, want 4", replicas, ok, err)
		}
		win := &egv1a1.EvictionGuardWindow{}
		if err := k8sClient.Get(ctx, winName, win); err != nil {
			return err
		}
		if len(win.Spec.Actions) != 2 {
			return fmt.Errorf("actions=%d, want 2", len(win.Spec.Actions))
		}
		byKey := map[string]egv1a1.ScaleAction{}
		for _, a := range win.Spec.Actions {
			byKey[a.Key] = a
		}
		if byKey["widget"].Baseline != 2 || byKey["widget"].ScaledTo != 4 {
			return fmt.Errorf("widget action=%+v", byKey["widget"])
		}
		if byKey["widget"].FieldPath != "spec.replicas" || byKey["widget"].Kind != "Widget" {
			return fmt.Errorf("widget action meta=%+v", byKey["widget"])
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
				NodeName:   "widget-worker-2",
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

	if err := k8sClient.Delete(ctx, atRisk); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: "widget-worker-1"}, node); err != nil {
		t.Fatal(err)
	}
	node.Spec.Taints = nil
	if err := k8sClient.Update(ctx, node); err != nil {
		t.Fatal(err)
	}

	eventually(t, 25*time.Second, func(ctx context.Context) error {
		got := &appsv1.Deployment{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(dep), got); err != nil {
			return err
		}
		if got.Spec.Replicas == nil || *got.Spec.Replicas != 3 {
			return fmt.Errorf("deployment replicas=%v, want 3 after restore", got.Spec.Replicas)
		}
		w := &unstructured.Unstructured{}
		w.SetGroupVersionKind(widgetGVK)
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: "web"}, w); err != nil {
			return err
		}
		replicas, ok, err := unstructured.NestedInt64(w.Object, "spec", "replicas")
		if err != nil || !ok || replicas != 2 {
			return fmt.Errorf("widget replicas=%v ok=%v err=%v, want 2 after restore", replicas, ok, err)
		}
		win := &egv1a1.EvictionGuardWindow{}
		err = k8sClient.Get(ctx, winName, win)
		if client.IgnoreNotFound(err) != nil {
			return err
		}
		if err == nil && win.Status.Phase != egv1a1.WindowPhaseCooling && win.Status.Phase != egv1a1.WindowPhaseClosed {
			return fmt.Errorf("window phase=%s, want Cooling or gone", win.Status.Phase)
		}
		return nil
	})
}
