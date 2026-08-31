/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package v1alpha1

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const DefaultScaleBackAfter = 15 * time.Minute

// WindowPhase is the lifecycle of a disruption window.
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

	ReasonReplicasReady   = "ReplicasReady"
	ReasonWaitingForReady = "WaitingForReady"
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

// ScaleAction is one object Eviction Guard patched for a window (Deployment, HPA, or CR).
type ScaleAction struct {
	Backend ScaleBackendType `json:"backend"`

	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// FieldPath is used by the crd backend (default spec.replicas).
	FieldPath string `json:"fieldPath,omitempty"`

	// Baseline is this object's value before scale-up.
	Baseline int32 `json:"baseline"`

	// ScaledTo is the value Eviction Guard set on this object.
	ScaledTo int32 `json:"scaledTo"`

	// StampKeys are annotation keys written onto the scaled object; scale-back deletes them.
	// +listType=atomic
	StampKeys []string `json:"stampKeys,omitempty"`
}

// ProactiveWindowSpec is the desired disruption-window record.
type ProactiveWindowSpec struct {
	// PolicyName is the EvictionGuardPolicy that opened this window.
	// +kubebuilder:validation:MinLength=1
	PolicyName string `json:"policyName"`

	// Target is the workload whose capacity was raised.
	Target WorkloadReference `json:"target"`

	// Backend is the primary scaling backend (first action). Kept for kubectl columns
	// and older windows that have no spec.actions.
	Backend ScaleBackendType `json:"backend"`

	// BackendTarget, if set, is the object the primary backend patches.
	BackendTarget *corev1.ObjectReference `json:"backendTarget,omitempty"`

	// Actions is the full fan-out: Deployment and/or HPA and/or CR, each with its
	// own baseline so scale-back restores every object independently.
	Actions []ScaleAction `json:"actions,omitempty"`

	// Baseline is the primary backend's replica count before scale-up.
	Baseline int32 `json:"baseline"`

	// ScaledTo is the replica (or minReplicas) count Eviction Guard set.
	ScaledTo int32 `json:"scaledTo"`

	// VulnerableNodes are the node names that triggered this window.
	VulnerableNodes []string `json:"vulnerableNodes,omitempty"`

	// WindowUntil is when scale-back becomes eligible. Set only after vulnerable
	// nodes have cleared and status.spareReady is true (Cooling). Empty while Open.
	WindowUntil *metav1.Time `json:"windowUntil,omitempty"`
}

// ProactiveWindowStatus is observed by Eviction Guard and by other plugins.
type ProactiveWindowStatus struct {
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
// +kubebuilder:printcolumn:name="Backend",type=string,JSONPath=.spec.backend
// +kubebuilder:printcolumn:name="Baseline",type=integer,JSONPath=.spec.baseline
// +kubebuilder:printcolumn:name="ScaledTo",type=integer,JSONPath=.spec.scaledTo
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=.status.phase
// +kubebuilder:printcolumn:name="SpareReady",type=boolean,JSONPath=.status.spareReady
// +kubebuilder:printcolumn:name="Until",type=string,JSONPath=.spec.windowUntil
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=.metadata.creationTimestamp

// ProactiveWindow records a temporary capacity increase for one workload under one policy.
// Other Kubernetes plugins MAY watch this resource to observe (or compose with) Eviction Guard's actions.
type ProactiveWindow struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProactiveWindowSpec   `json:"spec,omitempty"`
	Status ProactiveWindowStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ProactiveWindowList contains a list of ProactiveWindow.
type ProactiveWindowList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProactiveWindow `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProactiveWindow{}, &ProactiveWindowList{})
}

func (w *ProactiveWindow) IsActive() bool {
	switch w.Status.Phase {
	case WindowPhaseOpen, WindowPhaseCooling, WindowPhaseHeld, "":
		return true
	default:
		return false
	}
}

// ScaleActions is spec.actions, or a single synthetic action for windows
// created before multi-backend (spec.backend + spec.backendTarget).
func (w *ProactiveWindow) ScaleActions() []ScaleAction {
	if len(w.Spec.Actions) > 0 {
		return w.Spec.Actions
	}
	a := ScaleAction{
		Backend:    w.Spec.Backend,
		APIVersion: w.Spec.Target.APIVersion,
		Kind:       w.Spec.Target.Kind,
		Namespace:  w.Spec.Target.Namespace,
		Name:       w.Spec.Target.Name,
		Baseline:   w.Spec.Baseline,
		ScaledTo:   w.Spec.ScaledTo,
	}
	if w.Spec.BackendTarget != nil && w.Spec.BackendTarget.Name != "" {
		a.APIVersion = w.Spec.BackendTarget.APIVersion
		a.Kind = w.Spec.BackendTarget.Kind
		a.Namespace = w.Spec.BackendTarget.Namespace
		a.Name = w.Spec.BackendTarget.Name
	}
	return []ScaleAction{a}
}
