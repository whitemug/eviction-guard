/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

func TestMaxWindowExceeded(t *testing.T) {
	opened := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	win := &egv1a1.ProactiveWindow{
		Status: egv1a1.ProactiveWindowStatus{LastScaleTime: &metav1.Time{Time: opened}},
	}
	def := &egv1a1.EvictionGuardPolicy{}
	if maxWindowExceeded(def, win, opened.Add(time.Hour)) {
		t.Fatal("default 2h should not expire at 1h")
	}
	if !maxWindowExceeded(def, win, opened.Add(2*time.Hour)) {
		t.Fatal("default 2h should expire at 2h")
	}
	capped := &egv1a1.EvictionGuardPolicy{Spec: egv1a1.EvictionGuardPolicySpec{
		MaxWindow: &metav1.Duration{Duration: 30 * time.Minute},
	}}
	if !maxWindowExceeded(capped, win, opened.Add(30*time.Minute)) {
		t.Fatal("30m cap should expire at 30m")
	}
	unlimited := &egv1a1.EvictionGuardPolicy{Spec: egv1a1.EvictionGuardPolicySpec{
		MaxWindow: &metav1.Duration{Duration: 0},
	}}
	if maxWindowExceeded(unlimited, win, opened.Add(24*time.Hour)) {
		t.Fatal("0 means unlimited")
	}
}

func TestHoldMaxWindow(t *testing.T) {
	win := &egv1a1.ProactiveWindow{Status: egv1a1.ProactiveWindowStatus{ForcedCool: true}}
	if !holdMaxWindow(&egv1a1.EvictionGuardPolicy{}, win, time.Now()) {
		t.Fatal("ForcedCool should hold")
	}
	closed := &egv1a1.ProactiveWindow{Status: egv1a1.ProactiveWindowStatus{Phase: egv1a1.WindowPhaseClosed}}
	if !holdMaxWindow(&egv1a1.EvictionGuardPolicy{}, closed, time.Now()) {
		t.Fatal("Closed should hold")
	}
}
