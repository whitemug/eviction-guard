/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/metrics"
)

// scaleErrorClass is a Kind-agnostic classification of capacity mutation failures.
type scaleErrorClass string

const (
	scaleErrorNone      scaleErrorClass = ""
	scaleErrorForbidden scaleErrorClass = "forbidden"
	scaleErrorRejected  scaleErrorClass = "rejected" // Invalid / BadRequest / CauseTypeFieldValue*
	scaleErrorConflict  scaleErrorClass = "conflict"
	scaleErrorNotFound  scaleErrorClass = "notfound"
	scaleErrorOther     scaleErrorClass = "other"
)

func classifyScaleError(err error) scaleErrorClass {
	if err == nil {
		return scaleErrorNone
	}
	if isMissingBackendRBAC(err) || apierrors.IsForbidden(err) {
		return scaleErrorForbidden
	}
	if apierrors.IsConflict(err) {
		return scaleErrorConflict
	}
	if apierrors.IsNotFound(err) {
		return scaleErrorNotFound
	}
	if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
		return scaleErrorRejected
	}
	var status apierrors.APIStatus
	if errors.As(err, &status) && status.Status().Details != nil {
		for _, c := range status.Status().Details.Causes {
			switch c.Type {
			case metav1.CauseTypeFieldValueInvalid, metav1.CauseTypeFieldValueDuplicate,
				metav1.CauseTypeFieldValueNotFound, metav1.CauseTypeFieldValueRequired,
				metav1.CauseTypeFieldValueNotSupported, metav1.CauseTypeTooLong,
				metav1.CauseTypeForbidden, metav1.CauseTypeTypeInvalid:
				return scaleErrorRejected
			}
		}
	}
	return scaleErrorOther
}

func scaleErrorReason(class scaleErrorClass) string {
	switch class {
	case scaleErrorForbidden:
		return egv1a1.ReasonScaleForbidden
	case scaleErrorRejected:
		return egv1a1.ReasonScaleRejected
	case scaleErrorConflict:
		return egv1a1.ReasonScaleConflict
	case scaleErrorNotFound:
		return egv1a1.ReasonScaleMissing
	case scaleErrorOther:
		return egv1a1.ReasonScaleFailed
	default:
		return egv1a1.ReasonScaleApplied
	}
}

// applyCapacityApplied sets ConditionCapacityApplied from the last scale mutation error.
// err == nil means capacity patches applied (or there was nothing to patch).
// Returns true when the condition status or reason flipped (or was first written).
func applyCapacityApplied(win *egv1a1.EvictionGuardWindow, err error, now metav1.Time) (transitioned bool) {
	if win == nil {
		return false
	}
	class := classifyScaleError(err)
	wantOK := class == scaleErrorNone
	prev := meta.FindStatusCondition(win.Status.Conditions, egv1a1.ConditionCapacityApplied)
	reason := scaleErrorReason(class)
	transitioned = prev == nil ||
		(prev.Status == metav1.ConditionTrue) != wantOK ||
		prev.Reason != reason

	cond := metav1.Condition{
		Type:               egv1a1.ConditionCapacityApplied,
		ObservedGeneration: win.Generation,
		LastTransitionTime: now,
	}
	labels := []string{win.Spec.PolicyName, win.Spec.Target.Namespace, win.Spec.Target.Name}
	if wantOK {
		cond.Status = metav1.ConditionTrue
		cond.Reason = egv1a1.ReasonScaleApplied
		cond.Message = "capacity patches applied"
		metrics.CapacityApplyError.WithLabelValues(labels...).Set(0)
	} else {
		cond.Status = metav1.ConditionFalse
		cond.Reason = reason
		cond.Message = fmt.Sprintf("%s; eviction fail-opens after maxWindow", err.Error())
		metrics.CapacityApplyError.WithLabelValues(labels...).Set(1)
	}
	meta.SetStatusCondition(&win.Status.Conditions, cond)
	return transitioned
}

func capacityAppliedBlocked(win *egv1a1.EvictionGuardWindow) bool {
	if win == nil {
		return false
	}
	c := meta.FindStatusCondition(win.Status.Conditions, egv1a1.ConditionCapacityApplied)
	return c != nil && c.Status == metav1.ConditionFalse
}

func applyErrorMessage(base string, win *egv1a1.EvictionGuardWindow) string {
	if win == nil || !capacityAppliedBlocked(win) {
		return base
	}
	c := meta.FindStatusCondition(win.Status.Conditions, egv1a1.ConditionCapacityApplied)
	if c == nil || c.Message == "" {
		return base + "; capacity apply failed (fail-open after maxWindow)"
	}
	return fmt.Sprintf("%s; %s", base, c.Message)
}
