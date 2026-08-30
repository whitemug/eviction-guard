/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package backends_test

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/backends"
)

func TestDeploymentScale(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	replicas := int32(3)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dep).Build()
	b := &backends.DeploymentBackend{}
	tgt := backends.Target{ObjectKey: client.ObjectKey{Namespace: "app", Name: "web"}}

	cur, err := b.Current(context.Background(), c, tgt)
	if err != nil || cur != 3 {
		t.Fatalf("Current=%d err=%v", cur, err)
	}
	if err := b.ScaleUp(context.Background(), c, tgt, 5); err != nil {
		t.Fatal(err)
	}
	got := &appsv1.Deployment{}
	if err := c.Get(context.Background(), tgt.ObjectKey, got); err != nil {
		t.Fatal(err)
	}
	if *got.Spec.Replicas != 5 {
		t.Fatalf("replicas=%d", *got.Spec.Replicas)
	}
}

func TestPatchAnnotationsOnDeployment(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	replicas := int32(3)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dep).Build()
	b := &backends.DeploymentBackend{}
	tgt := backends.Target{
		ObjectKey:  client.ObjectKey{Namespace: "app", Name: "web"},
		APIVersion: "apps/v1",
		Kind:       "Deployment",
	}
	ctx := context.Background()
	if err := b.ScaleUp(ctx, c, tgt, 5); err != nil {
		t.Fatal(err)
	}
	stamps := map[string]string{
		"eviction-guard.io/scaled-to": "5",
		"eviction-guard.io/baseline":  "3",
		"eviction-guard.io/active":    "true",
	}
	if err := backends.PatchAnnotations(ctx, c, tgt, stamps); err != nil {
		t.Fatal(err)
	}
	got := &appsv1.Deployment{}
	if err := c.Get(ctx, tgt.ObjectKey, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations["eviction-guard.io/scaled-to"] != "5" || got.Annotations["eviction-guard.io/active"] != "true" {
		t.Fatalf("annotations=%v", got.Annotations)
	}
	if err := b.ScaleDown(ctx, c, tgt, 3); err != nil {
		t.Fatal(err)
	}
	if err := backends.PatchAnnotations(ctx, c, tgt, backends.StampDeletes([]string{
		"eviction-guard.io/scaled-to",
		"eviction-guard.io/baseline",
		"eviction-guard.io/active",
	})); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, tgt.ObjectKey, got); err != nil {
		t.Fatal(err)
	}
	if len(got.Annotations) != 0 {
		t.Fatalf("expected stamps cleared, got %v", got.Annotations)
	}
}

func TestHPAMinDoesNotScaleBelowCurrent(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = autoscalingv1.AddToScheme(scheme)
	min := int32(4)
	hpa := &autoscalingv1.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec:       autoscalingv1.HorizontalPodAutoscalerSpec{MinReplicas: &min, MaxReplicas: 10},
		Status:     autoscalingv1.HorizontalPodAutoscalerStatus{CurrentReplicas: 6},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(hpa).Build()
	b := &backends.HPAMinBackend{}
	tgt := backends.Target{ObjectKey: client.ObjectKey{Namespace: "app", Name: "web"}}
	if err := b.ScaleDown(context.Background(), c, tgt, 3); err != nil {
		t.Fatal(err)
	}
	got := &autoscalingv1.HorizontalPodAutoscaler{}
	if err := c.Get(context.Background(), tgt.ObjectKey, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.MinReplicas == nil || *got.Spec.MinReplicas != 6 {
		t.Fatalf("minReplicas=%v, want 6 (held at currentReplicas)", got.Spec.MinReplicas)
	}
}

func TestParseScaleTarget(t *testing.T) {
	t1, err := backends.ParseScaleTarget("app/web-hpa", "default")
	if err != nil || t1.Namespace != "app" || t1.Name != "web-hpa" {
		t.Fatalf("got %+v err=%v", t1, err)
	}
	t2, err := backends.ParseScaleTarget("example.com/v1/namespaces/app/Widget/web", "")
	if err != nil || t2.APIVersion != "example.com/v1" || t2.Kind != "Widget" || t2.Name != "web" {
		t.Fatalf("got %+v err=%v", t2, err)
	}
}

func TestParseList(t *testing.T) {
	got, err := backends.ParseList("deployment, hpa-min,crd")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "deployment" || got[1] != "hpa-min" || got[2] != "crd" {
		t.Fatalf("got %v", got)
	}
	up := backends.SortForScaleUp([]egv1a1.ScaleBackendType{"crd", "hpa-min", "deployment"})
	if up[0] != "deployment" || up[1] != "hpa-min" || up[2] != "crd" {
		t.Fatalf("scale-up order %v", up)
	}
	down := backends.SortForScaleDown([]egv1a1.ScaleBackendType{"crd", "deployment", "hpa-min"})
	if down[0] != "hpa-min" || down[1] != "deployment" || down[2] != "crd" {
		t.Fatalf("scale-down order %v", down)
	}
	if _, err := backends.ParseList("deployment,nope"); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestPatchAnnotationsOnCRD(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "example.com/v1",
		"kind":       "Widget",
		"metadata": map[string]interface{}{
			"name":      "web",
			"namespace": "app",
		},
		"spec": map[string]interface{}{"replicas": int64(3)},
	}}
	c := fake.NewClientBuilder().WithObjects(obj.DeepCopy()).Build()
	b := &backends.CRDBackend{}
	tgt := backends.Target{
		ObjectKey:  client.ObjectKey{Namespace: "app", Name: "web"},
		APIVersion: "example.com/v1",
		Kind:       "Widget",
		FieldPath:  "spec.replicas",
	}
	ctx := context.Background()
	if err := b.ScaleUp(ctx, c, tgt, 5); err != nil {
		t.Fatal(err)
	}
	if err := backends.PatchAnnotations(ctx, c, tgt, map[string]string{
		"eviction-guard.io/scaled-to":    "5",
		"eviction-guard.io/baseline":     "3",
		"eviction-guard.io/active":       "true",
		"eviction-guard.io/window-until": "2026-08-30T12:15:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	got := &unstructured.Unstructured{}
	got.SetAPIVersion("example.com/v1")
	got.SetKind("Widget")
	if err := c.Get(ctx, tgt.ObjectKey, got); err != nil {
		t.Fatal(err)
	}
	v, _, _ := unstructured.NestedInt64(got.Object, "spec", "replicas")
	if v != 5 {
		t.Fatalf("replicas=%d", v)
	}
	if got.GetAnnotations()["eviction-guard.io/scaled-to"] != "5" || got.GetAnnotations()["eviction-guard.io/active"] != "true" {
		t.Fatalf("annotations=%v", got.GetAnnotations())
	}

	if err := b.ScaleDown(ctx, c, tgt, 3); err != nil {
		t.Fatal(err)
	}
	if err := backends.PatchAnnotations(ctx, c, tgt, backends.StampDeletes([]string{
		"eviction-guard.io/scaled-to",
		"eviction-guard.io/baseline",
		"eviction-guard.io/active",
		"eviction-guard.io/window-until",
	})); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, tgt.ObjectKey, got); err != nil {
		t.Fatal(err)
	}
	v, _, _ = unstructured.NestedInt64(got.Object, "spec", "replicas")
	if v != 3 {
		t.Fatalf("replicas after down=%d", v)
	}
	if len(got.GetAnnotations()) != 0 {
		t.Fatalf("expected stamp annotations cleared, got %v", got.GetAnnotations())
	}
}
