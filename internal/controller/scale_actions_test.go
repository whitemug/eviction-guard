/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

func TestResolveActionTargetKeepsHPAAndCRDDistinct(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "app",
			Annotations: map[string]string{
				egv1a1.HPATargetAnnotation:       "app/web-hpa",
				egv1a1.ScaleTargetAnnotation:     "example.com/v1/namespaces/app/Widget/web",
				egv1a1.CRDReplicasPathAnnotation: "spec.spare",
			},
		},
	}
	hpa, err := resolveActionTarget(dep, egv1a1.ScaleBackendHPAMin)
	if err != nil {
		t.Fatal(err)
	}
	if hpa.Name != "web-hpa" || hpa.Kind != "HorizontalPodAutoscaler" {
		t.Fatalf("hpa target %+v", hpa)
	}
	crd, err := resolveActionTarget(dep, egv1a1.ScaleBackendCRD)
	if err != nil {
		t.Fatal(err)
	}
	if crd.Kind != "Widget" || crd.APIVersion != "example.com/v1" || crd.FieldPath != "spec.spare" {
		t.Fatalf("crd target %+v", crd)
	}
	deploy, err := resolveActionTarget(dep, egv1a1.ScaleBackendDeployment)
	if err != nil {
		t.Fatal(err)
	}
	if deploy.Name != "web" || deploy.Kind != "Deployment" {
		t.Fatalf("deploy target %+v", deploy)
	}
}

func TestResolveActionTargetPluginRequiresScaleTarget(t *testing.T) {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"}}
	if _, err := resolveActionTarget(dep, "custom"); err == nil {
		t.Fatal("expected error without scale-target")
	}
	dep.Annotations = map[string]string{egv1a1.ScaleTargetAnnotation: "app/web"}
	if _, err := resolveActionTarget(dep, "custom"); err == nil {
		t.Fatal("expected error for ns/name without GVK")
	}
	dep.Annotations[egv1a1.ScaleTargetAnnotation] = "example.com/v1/namespaces/app/Widget/web"
	dep.Annotations[egv1a1.CRDReplicasPathAnnotation] = "spec.spare"
	got, err := resolveActionTarget(dep, "custom")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "Widget" || got.FieldPath != "spec.spare" {
		t.Fatalf("custom target %+v", got)
	}
}

func TestScaleBackAfterWorkloadOverride(t *testing.T) {
	policy := &egv1a1.EvictionGuardPolicy{}
	if d := scaleBackAfter(policy, nil); d != egv1a1.DefaultScaleBackAfter {
		t.Fatalf("default=%s", d)
	}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		egv1a1.ScaleBackAfterAnnotation: "5m",
	}}}
	if d := scaleBackAfter(policy, dep); d != 5*time.Minute {
		t.Fatalf("override=%s", d)
	}
}

func TestResolveStampKeys(t *testing.T) {
	policy := &egv1a1.EvictionGuardPolicy{Spec: egv1a1.EvictionGuardPolicySpec{
		Stamp: egv1a1.StampSpec{Enabled: true, ScaledToKey: "example.com/desired"},
	}}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		egv1a1.StampBaselineKeyAnnotation: "example.com/from",
	}}}
	s := resolveStamp(policy, dep)
	if !s.enabled || s.scaledTo != "example.com/desired" || s.baseline != "example.com/from" {
		t.Fatalf("%+v", s)
	}
	dep.Annotations[egv1a1.StampAnnotation] = "false"
	if resolveStamp(policy, dep).enabled {
		t.Fatal("workload false should disable")
	}
	dep.Annotations = map[string]string{egv1a1.StampAnnotation: "true"}
	if !resolveStamp(policy, dep).enabled {
		t.Fatal("workload true should enable")
	}
}

func TestStampValuesClearWindowUntilWhileOpen(t *testing.T) {
	s := resourceStamp{
		enabled:     true,
		scaledTo:    egv1a1.StampScaledToKey,
		baseline:    egv1a1.StampBaselineKey,
		active:      egv1a1.StampActiveKey,
		windowUntil: egv1a1.StampWindowUntilKey,
	}
	open := s.values(3, 4)
	if open[egv1a1.StampScaledToKey] != "4" || open[egv1a1.StampWindowUntilKey] != "" {
		t.Fatalf("open stamps=%v, want window-until cleared", open)
	}
}
