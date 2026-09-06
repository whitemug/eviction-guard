/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

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
		t.Fatal("stamp override false")
	}
	dep.Annotations = map[string]string{egv1a1.StampAnnotation: "true"}
	if !resolveStamp(policy, dep).enabled {
		t.Fatal("stamp override true")
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
