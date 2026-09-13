/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package backends_test

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/whitemug/eviction-guard/pkg/backends"
)

func TestFieldPatchDeployment(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	replicas := int32(3)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dep).Build()
	tgt := backends.Target{
		ObjectKey:  client.ObjectKey{Namespace: "app", Name: "web"},
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		FieldPath:  "spec.replicas",
	}

	cur, err := backends.Current(context.Background(), c, tgt)
	if err != nil || cur != 3 {
		t.Fatalf("Current=%d err=%v", cur, err)
	}
	if err := backends.ScaleUp(context.Background(), c, tgt, 5); err != nil {
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
	tgt := backends.Target{
		ObjectKey:  client.ObjectKey{Namespace: "app", Name: "web"},
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		FieldPath:  "spec.replicas",
	}
	ctx := context.Background()
	if err := backends.ScaleUp(ctx, c, tgt, 5); err != nil {
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
	if err := backends.ScaleDown(ctx, c, tgt, 3); err != nil {
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

func TestParseBindingsOrderPreserved(t *testing.T) {
	got, err := backends.ParseBindings("hpa,deployment,webapp=x", "ns", "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Key != "hpa" || got[1].Key != "deployment" || got[2].Key != "webapp" {
		t.Fatalf("order=%+v", got)
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
		"spec": map[string]interface{}{
			"replicas": int64(2),
		},
	}}
	scheme := runtime.NewScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	tgt := backends.Target{
		ObjectKey:  client.ObjectKey{Namespace: "app", Name: "web"},
		APIVersion: "example.com/v1",
		Kind:       "Widget",
		FieldPath:  "spec.replicas",
	}
	ctx := context.Background()
	if err := backends.ScaleUp(ctx, c, tgt, 4); err != nil {
		t.Fatal(err)
	}
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(obj.GroupVersionKind())
	if err := c.Get(ctx, tgt.ObjectKey, got); err != nil {
		t.Fatal(err)
	}
	v, _, _ := unstructured.NestedInt64(got.Object, "spec", "replicas")
	if v != 4 {
		t.Fatalf("replicas=%d", v)
	}
	if err := backends.PatchAnnotations(ctx, c, tgt, map[string]string{"example.com/active": "true"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, tgt.ObjectKey, got); err != nil {
		t.Fatal(err)
	}
	anns, _, _ := unstructured.NestedStringMap(got.Object, "metadata", "annotations")
	if anns["example.com/active"] != "true" {
		t.Fatalf("annotations=%v", anns)
	}
	if err := backends.ScaleDown(ctx, c, tgt, 2); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, tgt.ObjectKey, got); err != nil {
		t.Fatal(err)
	}
	v, _, _ = unstructured.NestedInt64(got.Object, "spec", "replicas")
	if v != 2 {
		t.Fatalf("replicas=%d after scale-down", v)
	}
}
