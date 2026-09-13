/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/backends"
)

func TestWrapBackendErrForbidden(t *testing.T) {
	tgt := backends.Target{
		ObjectKey:  client.ObjectKey{Namespace: "app", Name: "fireship"},
		APIVersion: "example.com/v1",
		Kind:       "WebApp",
	}
	err := wrapBackendErr(tgt, apierrors.NewForbidden(schema.GroupResource{Group: "example.com", Resource: "webapps"}, "fireship", errors.New("denied")))
	if !isMissingBackendRBAC(err) {
		t.Fatalf("want missing-RBAC wrapper, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing RBAC") || !strings.Contains(msg, "WebApp") || !strings.Contains(msg, "extraClusterRoleRules") {
		t.Fatalf("message=%q", msg)
	}
	if isMissingBackendRBAC(wrapBackendErr(tgt, errors.New("not found"))) {
		t.Fatal("non-Forbidden must not look like missing RBAC")
	}
}

func TestBuildPlanExternalOnly(t *testing.T) {
	policy := &egv1a1.EvictionGuardPolicy{
		Spec: egv1a1.EvictionGuardPolicySpec{
			SpareReplicas: ptr.To(int32(1)),
			MaxBuffer:     ptr.To(int32(4)),
			Backends: map[string]egv1a1.BackendCatalogEntry{
				"scaledobject": {
					APIVersion: "keda.sh/v1alpha1",
					Kind:       "ScaledObject",
				},
			},
		},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "app",
			Annotations: map[string]string{
				egv1a1.ScaleBackendAnnotation: "scaledobject",
			},
		},
		Spec: appsv1.DeploymentSpec{Replicas: ptr.To(int32(3))},
	}
	c := fake.NewClientBuilder().Build()
	plan, err := buildPlan(context.Background(), c, policy, dep, 2, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.steps) != 0 {
		t.Fatalf("steps=%+v", plan.steps)
	}
	if plan.primaryBaseline != 3 {
		t.Fatalf("baseline=%d want 3", plan.primaryBaseline)
	}
	// baseline 3 + atRisk 2 with spareReplicas 1 → desired 5
	if plan.desired != 5 {
		t.Fatalf("desired=%d want 5", plan.desired)
	}
	if actions := plan.actions(nil); len(actions) != 0 {
		t.Fatalf("actions=%+v", actions)
	}
	plan2, err := buildPlan(context.Background(), c, policy, dep, 2, nil, 7)
	if err != nil {
		t.Fatal(err)
	}
	if plan2.primaryBaseline != 7 {
		t.Fatalf("sticky baseline=%d", plan2.primaryBaseline)
	}
}

func TestRestoreGroupedActionsUsesBaseline(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := egv1a1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	hpa := &autoscalingv1.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: autoscalingv1.HorizontalPodAutoscalerSpec{
			MinReplicas: ptr.To(int32(5)),
			MaxReplicas: 12,
			ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{
				Kind: "Deployment", Name: "web", APIVersion: "apps/v1",
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&autoscalingv1.HorizontalPodAutoscaler{}).
		WithObjects(hpa).Build()

	win := &egv1a1.EvictionGuardWindow{
		ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "app"},
		Spec: egv1a1.EvictionGuardWindowSpec{
			Target: egv1a1.WorkloadReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", Namespace: "app"},
			Actions: []egv1a1.ScaleAction{
				{
					APIVersion: "autoscaling/v1", Kind: "HorizontalPodAutoscaler",
					Name: "web", Namespace: "app", FieldPath: "spec.minReplicas",
					Baseline: 2, ScaledTo: 5,
				},
				{
					APIVersion: "autoscaling/v1", Kind: "HorizontalPodAutoscaler",
					Name: "web", Namespace: "app", FieldPath: "spec.maxReplicas",
					Baseline: 10, ScaledTo: 12,
				},
			},
		},
	}
	if err := restoreActions(context.Background(), c, win); err != nil {
		t.Fatal(err)
	}
	got := &autoscalingv1.HorizontalPodAutoscaler{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "app", Name: "web"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.MinReplicas == nil || *got.Spec.MinReplicas != 2 {
		t.Fatalf("minReplicas=%v, want baseline 2", got.Spec.MinReplicas)
	}
	if got.Spec.MaxReplicas != 10 {
		t.Fatalf("maxReplicas=%d, want baseline 10", got.Spec.MaxReplicas)
	}
}

func TestRestoreActionsSkipsMissingDeployContinuesHPA(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := egv1a1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	hpa := &autoscalingv1.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: autoscalingv1.HorizontalPodAutoscalerSpec{
			MinReplicas: ptr.To(int32(5)),
			MaxReplicas: 10,
			ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{
				Kind: "Deployment", Name: "web", APIVersion: "apps/v1",
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&autoscalingv1.HorizontalPodAutoscaler{}).
		WithObjects(hpa).Build()

	win := &egv1a1.EvictionGuardWindow{
		ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "app"},
		Spec: egv1a1.EvictionGuardWindowSpec{
			Target: egv1a1.WorkloadReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", Namespace: "app"},
			Actions: []egv1a1.ScaleAction{
				{
					APIVersion: "apps/v1", Kind: "Deployment",
					Name: "web", Namespace: "app", FieldPath: "spec.replicas",
					Baseline: 3, ScaledTo: 5,
				},
				{
					APIVersion: "autoscaling/v1", Kind: "HorizontalPodAutoscaler",
					Name: "web", Namespace: "app", FieldPath: "spec.minReplicas",
					Baseline: 2, ScaledTo: 5,
				},
			},
		},
	}
	if err := restoreActions(context.Background(), c, win); err != nil {
		t.Fatal(err)
	}
	got := &autoscalingv1.HorizontalPodAutoscaler{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "app", Name: "web"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.MinReplicas == nil || *got.Spec.MinReplicas != 2 {
		t.Fatalf("minReplicas=%v, want baseline 2 after skipping missing Deployment", got.Spec.MinReplicas)
	}
}

func TestRestoreActionsHonorsSkipDownscaling(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := egv1a1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(5))},
	}
	hpa := &autoscalingv1.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: autoscalingv1.HorizontalPodAutoscalerSpec{
			MinReplicas: ptr.To(int32(5)),
			MaxReplicas: 10,
			ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{
				Kind: "Deployment", Name: "web", APIVersion: "apps/v1",
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&appsv1.Deployment{}, &autoscalingv1.HorizontalPodAutoscaler{}).
		WithObjects(dep, hpa).Build()

	win := &egv1a1.EvictionGuardWindow{
		ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "app"},
		Spec: egv1a1.EvictionGuardWindowSpec{
			Target: egv1a1.WorkloadReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "web", Namespace: "app"},
			Actions: []egv1a1.ScaleAction{
				{
					APIVersion: "apps/v1", Kind: "Deployment",
					Name: "web", Namespace: "app", FieldPath: "spec.replicas",
					Baseline: 3, ScaledTo: 5, SkipDownscaling: true,
				},
				{
					APIVersion: "autoscaling/v1", Kind: "HorizontalPodAutoscaler",
					Name: "web", Namespace: "app", FieldPath: "spec.minReplicas",
					Baseline: 2, ScaledTo: 5,
				},
			},
		},
	}
	if err := restoreActions(context.Background(), c, win); err != nil {
		t.Fatal(err)
	}
	gotDep := &appsv1.Deployment{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "app", Name: "web"}, gotDep); err != nil {
		t.Fatal(err)
	}
	if gotDep.Spec.Replicas == nil || *gotDep.Spec.Replicas != 5 {
		t.Fatalf("replicas=%v, want 5 (skipDownscaling)", gotDep.Spec.Replicas)
	}
	gotHPA := &autoscalingv1.HorizontalPodAutoscaler{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "app", Name: "web"}, gotHPA); err != nil {
		t.Fatal(err)
	}
	if gotHPA.Spec.MinReplicas == nil || *gotHPA.Spec.MinReplicas != 2 {
		t.Fatalf("minReplicas=%v, want baseline 2", gotHPA.Spec.MinReplicas)
	}
}
