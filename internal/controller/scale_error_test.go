/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

func TestClassifyScaleError(t *testing.T) {
	if classifyScaleError(nil) != scaleErrorNone {
		t.Fatal("nil")
	}
	if classifyScaleError(apierrors.NewForbidden(schema.GroupResource{Resource: "hpa"}, "x", errors.New("no"))) != scaleErrorForbidden {
		t.Fatal("forbidden")
	}
	if classifyScaleError(apierrors.NewInvalid(schema.GroupKind{Kind: "HPA"}, "x", nil)) != scaleErrorRejected {
		t.Fatal("invalid")
	}
	if classifyScaleError(apierrors.NewBadRequest("bad")) != scaleErrorRejected {
		t.Fatal("bad request")
	}
	if classifyScaleError(apierrors.NewConflict(schema.GroupResource{Resource: "hpa"}, "x", errors.New("c"))) != scaleErrorConflict {
		t.Fatal("conflict")
	}
	if classifyScaleError(apierrors.NewNotFound(schema.GroupResource{Resource: "hpa"}, "x")) != scaleErrorNotFound {
		t.Fatal("notfound")
	}
	if classifyScaleError(errors.New("boom")) != scaleErrorOther {
		t.Fatal("other")
	}
}

func TestApplyCapacityApplied(t *testing.T) {
	now := metav1.Now()
	win := &egv1a1.EvictionGuardWindow{
		ObjectMeta: metav1.ObjectMeta{Name: "w", Namespace: "app", Generation: 1},
		Spec: egv1a1.EvictionGuardWindowSpec{
			PolicyName: "spot",
			Target:     egv1a1.WorkloadReference{Namespace: "app", Name: "web"},
			ScaledTo:   11,
		},
	}
	if !applyCapacityApplied(win, nil, now) {
		t.Fatal("first success should transition")
	}
	c := meta.FindStatusCondition(win.Status.Conditions, egv1a1.ConditionCapacityApplied)
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != egv1a1.ReasonScaleApplied {
		t.Fatalf("cond=%+v", c)
	}

	err := apierrors.NewInvalid(schema.GroupKind{Group: "autoscaling", Kind: "HorizontalPodAutoscaler"}, "web", nil)
	if !applyCapacityApplied(win, err, now) {
		t.Fatal("flip to failed should transition")
	}
	c = meta.FindStatusCondition(win.Status.Conditions, egv1a1.ConditionCapacityApplied)
	if c == nil || c.Status != metav1.ConditionFalse || c.Reason != egv1a1.ReasonScaleRejected {
		t.Fatalf("cond=%+v", c)
	}
	if !strings.Contains(c.Message, "maxWindow") {
		t.Fatalf("message should mention failover: %q", c.Message)
	}
	if applyCapacityApplied(win, err, now) {
		t.Fatal("same failure must not re-transition")
	}

	if !applyCapacityApplied(win, nil, now) {
		t.Fatal("clearing failure should transition")
	}
	c = meta.FindStatusCondition(win.Status.Conditions, egv1a1.ConditionCapacityApplied)
	if c == nil || c.Status != metav1.ConditionTrue || c.Reason != egv1a1.ReasonScaleApplied {
		t.Fatalf("cond=%+v", c)
	}

	msg := applyErrorMessage(msgWaitingSpare, win)
	if msg != msgWaitingSpare {
		t.Fatalf("unblocked message=%q", msg)
	}
	applyCapacityApplied(win, err, now)
	msg = applyErrorMessage(msgWaitingSpare, win)
	if !strings.Contains(msg, "maxWindow") {
		t.Fatalf("blocked message=%q", msg)
	}
}
