/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package v1alpha1

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const DefaultScaleBackAfter = time.Minute

// WindowPhase is the lifecycle of a disruption window.
// Held remains in the enum for compatibility with older Windows; new reconciles
// no longer enter Held (use backends[].skipDownscaling for sticky capacity).
// +kubebuilder:validation:Enum=Open;Cooling;Held;Closed
type WindowPhase string

const (
	WindowPhaseOpen    WindowPhase = "Open"
	WindowPhaseCooling WindowPhase = "Cooling"
	WindowPhaseHeld    WindowPhase = "Held"
	WindowPhaseClosed  WindowPhase = "Closed"

	// ConditionSpareReady is True when enough Ready pods sit off vulnerable nodes
	// to cover Spec.Baseline (G1: spare is actually usable).
	ConditionSpareReady = "SpareReady"

	// ConditionCapacityApplied is True when the last capacity mutation for this
	// window succeeded (or no patches were required). False means apply failed
	// (admission/RBAC/conflict/other) — Eviction Guard does not special-case
	// scaler kinds; eviction fail-opens after maxWindow (ForcedCool).
	ConditionCapacityApplied = "CapacityApplied"

	ReasonReplicasReady   = "ReplicasReady"
	ReasonWaitingForReady = "WaitingForReady"
	ReasonScaleApplied    = "Applied"
	ReasonScaleFailed     = "ApplyFailed"
	ReasonScaleRejected   = "Rejected"
	ReasonScaleForbidden  = "Forbidden"
	ReasonScaleConflict   = "Conflict"
	ReasonScaleMissing    = "Missing"
)

// WorkloadReference identifies the object whose capacity was raised.
type WorkloadReference struct {
	// APIVersion of the target (e.g. apps/v1).
	APIVersion string `json:"apiVersion"`
	// Kind of the target (e.g. Deployment).
	Kind string `json:"kind"`
	// Name of the target.
	Name string `json:"name"`
	// Namespace of the target.
	Namespace string `json:"namespace"`
}

// ScaleAction is one object Eviction Guard patched for a window.
// Actions are stored in eviction-guard.io/scale-backend token order for patched paths.
// Scale-up and scale-back walk that same order; actions[0] is the SpareReady primary
// when Actions is non-empty. Empty Actions means every bound catalog entry was external.
type ScaleAction struct {
	// Key is the policy.spec.backends catalog key (e.g. deployment, hpa, webapp).
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`

	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// FieldPath is the integer capacity field that was patched.
	FieldPath string `json:"fieldPath,omitempty"`

	// Baseline is this object's value before scale-up.
	Baseline int32 `json:"baseline"`

	// ScaledTo is the value Eviction Guard set on this object.
	ScaledTo int32 `json:"scaledTo"`

	// SkipDownscaling, when true, leaves this field at ScaledTo on scale-back
	// (copied from the catalog entry when the window opened). Stamps are still cleared.
	SkipDownscaling bool `json:"skipDownscaling,omitempty"`

	// StampKeys are annotation keys written onto the scaled object; scale-back deletes them.
	// +listType=atomic
	StampKeys []string `json:"stampKeys,omitempty"`
}

// EvictionGuardWindowSpec is the desired disruption-window record.
type EvictionGuardWindowSpec struct {
	// PolicyName is the EvictionGuardPolicy that opened this window.
	// +kubebuilder:validation:MinLength=1
	PolicyName string `json:"policyName"`

	// Target is the workload (Deployment) that owns the protected pods.
	Target WorkloadReference `json:"target"`

	// Actions is the capacity fan-out: each integer path patched for this window,
	// with its own baseline for independent restore. Empty when every bound catalog
	// entry is external (no path patches) — e.g. KEDA owns scaling.
	// +listType=atomic
	Actions []ScaleAction `json:"actions,omitempty"`

	// Baseline is the primary capacity before scale-up (first patched action, or the
	// Deployment replica count when Actions is empty). Used for SpareReady.
	Baseline int32 `json:"baseline"`

	// ScaledTo is the shared desired pod capacity for this window.
	ScaledTo int32 `json:"scaledTo"`

	// VulnerableNodes are the node names that triggered this window.
	VulnerableNodes []string `json:"vulnerableNodes,omitempty"`

	// WindowUntil is when the window may close. Set when Cooling starts, which is
	// also when capacity is restored (at-risk pods gone and spare Ready, or
	// maxWindow). Empty while Open.
	WindowUntil *metav1.Time `json:"windowUntil,omitempty"`
}

// EvictionGuardWindowStatus is observed by Eviction Guard and by other plugins.
type EvictionGuardWindowStatus struct {
	Phase WindowPhase `json:"phase,omitempty"`

	LastScaleTime *metav1.Time `json:"lastScaleTime,omitempty"`

	// SpareReady is true when SafeReadyReplicas >= Spec.Baseline.
	SpareReady bool `json:"spareReady,omitempty"`

	// ReadyReplicas is the Deployment's current Ready pod count.
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// SafeReadyReplicas is Ready pods whose node is not in Spec.VulnerableNodes.
	SafeReadyReplicas int32 `json:"safeReadyReplicas,omitempty"`

	// Message is a human-readable reason for the current phase.
	Message string `json:"message,omitempty"`

	// ForcedCool is true when cooldown started because maxWindow elapsed, not
	// because nodes cleared. The Closed object is kept as a tombstone until
	// those nodes are no longer vulnerable so the same disruption cannot reopen.
	ForcedCool bool `json:"forcedCool,omitempty"`

	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=egw,categories=eviction-guard
// +kubebuilder:printcolumn:name="Policy",type=string,JSONPath=.spec.policyName
// +kubebuilder:printcolumn:name="Key",type=string,JSONPath=.spec.actions[0].key
// +kubebuilder:printcolumn:name="Baseline",type=integer,JSONPath=.spec.baseline
// +kubebuilder:printcolumn:name="ScaledTo",type=integer,JSONPath=.spec.scaledTo
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=.status.phase
// +kubebuilder:printcolumn:name="SpareReady",type=boolean,JSONPath=.status.spareReady
// +kubebuilder:printcolumn:name="Until",type=string,JSONPath=.spec.windowUntil
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=.metadata.creationTimestamp

// EvictionGuardWindow records a temporary capacity increase for one workload under one policy.
// Other Kubernetes plugins MAY watch this resource to observe (or compose with) Eviction Guard's actions.
type EvictionGuardWindow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EvictionGuardWindowSpec   `json:"spec,omitempty"`
	Status EvictionGuardWindowStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EvictionGuardWindowList contains a list of EvictionGuardWindow.
type EvictionGuardWindowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EvictionGuardWindow `json:"items"`
}

func (w *EvictionGuardWindow) IsActive() bool {
	switch w.Status.Phase {
	case WindowPhaseOpen, WindowPhaseCooling, WindowPhaseHeld, "":
		return true
	default:
		return false
	}
}

// ScaleActions returns spec.actions (the only capacity fan-out record).
func (w *EvictionGuardWindow) ScaleActions() []ScaleAction {
	if w == nil {
		return nil
	}
	return w.Spec.Actions
}

// PrimaryAction is the first scale action (capacity primary for SpareReady math).
func (w *EvictionGuardWindow) PrimaryAction() *ScaleAction {
	if w == nil || len(w.Spec.Actions) == 0 {
		return nil
	}
	return &w.Spec.Actions[0]
}
