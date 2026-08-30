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

// ScaleBackendType is the capacity-mutation strategy Eviction Guard uses.
// +kubebuilder:validation:Enum=deployment;hpa-min;crd
type ScaleBackendType string

const (
	ScaleBackendDeployment ScaleBackendType = "deployment"
	ScaleBackendHPAMin     ScaleBackendType = "hpa-min"
	ScaleBackendCRD        ScaleBackendType = "crd"
)

// DisruptionSignal identifies a node condition that marks the node as about to evict pods.
// +kubebuilder:validation:Enum=KarpenterDisrupted;KarpenterDeleteRequested;OutOfService;SpotInterrupted;NodeCordoned
type DisruptionSignal string

const (
	SignalKarpenterDisrupted       DisruptionSignal = "KarpenterDisrupted"
	SignalKarpenterDeleteRequested DisruptionSignal = "KarpenterDeleteRequested"
	SignalOutOfService             DisruptionSignal = "OutOfService"
	SignalSpotInterrupted          DisruptionSignal = "SpotInterrupted"
	// SignalNodeCordoned is spec.unschedulable=true. Opt-in only — not a default.
	SignalNodeCordoned DisruptionSignal = "NodeCordoned"
)

// TaintMatch matches a node taint. Key is required; Value and Effect are optional filters.
type TaintMatch struct {
	// Key is the taint key to match.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`

	// Value, if set, must equal the taint value.
	Value string `json:"value,omitempty"`

	// Effect, if set, must equal the taint effect (NoSchedule, PreferNoSchedule, NoExecute).
	Effect corev1.TaintEffect `json:"effect,omitempty"`
}

// KeyValueMatch matches a node label or annotation. Key is required.
// If Value is empty, presence of the key is enough.
type KeyValueMatch struct {
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`

	// Value, if set, must equal the label/annotation value.
	Value string `json:"value,omitempty"`
}

// CustomDisruptionSignal is a user-defined node marker. Add one when you
// discover a cloud or vendor taint/annotation/label that is written *before*
// pods are evicted. No new Eviction Guard release is required.
//
// A signal is valid if it is (1) on the Node object, (2) set before drain,
// (3) specific to impending disruption, and (4) cleared when the node is done.
// At least one of taint, annotation, or label must be set; any of them matching
// fires the signal.
// +kubebuilder:validation:XValidation:rule="has(self.taint) || has(self.annotation) || has(self.label)",message="customSignals entry must set taint, annotation, or label"
type CustomDisruptionSignal struct {
	// Name is a short identifier for logs (e.g. GKEImpendingTermination).
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Taint matches a node taint. Key presence is enough unless Value/Effect are set.
	Taint *TaintMatch `json:"taint,omitempty"`

	// Annotation matches a node annotation. Key presence is enough unless Value is set.
	Annotation *KeyValueMatch `json:"annotation,omitempty"`

	// Label matches a node label. Key presence is enough unless Value is set.
	Label *KeyValueMatch `json:"label,omitempty"`
}

// NodeFilter selects a subset of cluster nodes for a policy to watch.
//
// Every non-empty field is applied, and the results are ANDed. An empty
// NodeFilter matches every node. Other plugins and operators customize
// coverage by creating EvictionGuardPolicy objects with different filters
// (for example: only Spot nodes, only a named node pool, only one AZ).
type NodeFilter struct {
	// LabelSelector selects nodes by labels (standard Kubernetes selector).
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`

	// ExcludeLabelSelector excludes nodes that match these labels, even if they
	// match LabelSelector.
	ExcludeLabelSelector *metav1.LabelSelector `json:"excludeLabelSelector,omitempty"`

	// Names is an explicit allow-list of node names. Empty means any name.
	Names []string `json:"names,omitempty"`

	// NamePattern is a glob matched against the node name (filepath.Match syntax),
	// e.g. "ip-10-0-*" or "spot-*".
	NamePattern string `json:"namePattern,omitempty"`

	// Zones restricts to topology.kubernetes.io/zone values.
	Zones []string `json:"zones,omitempty"`

	// InstanceTypes restricts to node.kubernetes.io/instance-type values.
	InstanceTypes []string `json:"instanceTypes,omitempty"`

	// CapacityTypes restricts to karpenter.sh/capacity-type values
	// (typically "spot" or "on-demand").
	CapacityTypes []string `json:"capacityTypes,omitempty"`

	// TaintSelector matches nodes that have all of the listed taint keys.
	// Value and Effect are optional additional constraints.
	TaintSelector []TaintMatch `json:"taintSelector,omitempty"`
}

// StampSpec configures visibility annotations written onto every object
// Eviction Guard patches (Deployment, HPA, and custom resources).
type StampSpec struct {
	// Enabled writes scaled-to/baseline/active on scale-up, window-until when
	// cooldown starts, and removes all of them on scale-back.
	Enabled bool `json:"enabled,omitempty"`

	// ScaledToKey is the annotation key for the raised count. Default: eviction-guard.io/scaled-to
	ScaledToKey string `json:"scaledToKey,omitempty"`
	// BaselineKey is the annotation key for the pre-scale value. Default: eviction-guard.io/baseline
	BaselineKey string `json:"baselineKey,omitempty"`
	// ActiveKey is set to "true" while the window is open. Default: eviction-guard.io/active
	ActiveKey string `json:"activeKey,omitempty"`
	// WindowUntilKey is RFC3339 scale-back eligibility time, written when cooldown
	// starts. Default: eviction-guard.io/window-until
	WindowUntilKey string `json:"windowUntilKey,omitempty"`
}

// EvictionGuardPolicySpec defines the desired state of EvictionGuardPolicy.
type EvictionGuardPolicySpec struct {
	// NodeFilter restricts which nodes this policy monitors.
	// Empty matches every node in the cluster.
	NodeFilter NodeFilter `json:"nodeFilter,omitempty"`

	// WorkloadSelector selects Deployments this policy may scale.
	// Defaults to {matchLabels: {"eviction-guard.io/enabled": "true"}}.
	WorkloadSelector *metav1.LabelSelector `json:"workloadSelector,omitempty"`

	// NamespaceSelector restricts which namespaces are considered.
	// Empty matches all namespaces.
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// RequirePDB, when true, only scales workloads that have a matching PodDisruptionBudget.
	// Defaults to true.
	RequirePDB *bool `json:"requirePDB,omitempty"`

	// SpareReplicas is the replica buffer added when at least one replica is at risk
	// (Strategy A in the design). Defaults to 1.
	// +kubebuilder:validation:Minimum=0
	SpareReplicas *int32 `json:"spareReplicas,omitempty"`

	// MaxBuffer caps how many extra replicas a single scale-up may add. Defaults to 4.
	// +kubebuilder:validation:Minimum=0
	MaxBuffer *int32 `json:"maxBuffer,omitempty"`

	// MaxConcurrentWindows caps how many Open/Cooling/Held ProactiveWindows this
	// policy may hold at once. At-risk workloads without a window wait until one
	// closes (scale-back finished). Defaults to 8. Set 0 for unlimited.
	// +kubebuilder:validation:Minimum=0
	MaxConcurrentWindows *int32 `json:"maxConcurrentWindows,omitempty"`

	// DefaultBackend is used when the workload does not set eviction-guard.io/scale-backend.
	// Defaults to "deployment". Combined with AdditionalBackends.
	DefaultBackend ScaleBackendType `json:"defaultBackend,omitempty"`

	// AdditionalBackends are applied together with DefaultBackend when the
	// workload annotation is unset. Typical pair: deployment + hpa-min.
	// If eviction-guard.io/scale-backend is set, it is the complete list.
	// +listType=atomic
	AdditionalBackends []ScaleBackendType `json:"additionalBackends,omitempty"`

	// ScaleBackAfter is the cooldown after vulnerable nodes have cleared and spare
	// pods are Ready off those nodes. Defaults to 15m.
	// A workload may override with eviction-guard.io/scale-back-after.
	ScaleBackAfter *metav1.Duration `json:"scaleBackAfter,omitempty"`

	// MaxWindow is how long a window may stay Open (nodes still vulnerable or
	// spare not Ready) before Eviction Guard force-cools and scales back.
	// Defaults to 2h. Set 0 for unlimited.
	MaxWindow *metav1.Duration `json:"maxWindow,omitempty"`

	// Stamp writes the same visibility annotations onto every scaled object
	// (Deployment, HPA, CR) so kubectl/GitOps/other operators can see baseline
	// and scaled-to without watching ProactiveWindow. Off by default.
	// Workload annotation eviction-guard.io/stamp: "true"|"false" overrides Enabled.
	Stamp StampSpec `json:"stamp,omitempty"`

	// DisruptionSignals lists which built-in node signals mark a node as vulnerable.
	// Empty enables the built-in defaults: KarpenterDisrupted, KarpenterDeleteRequested, OutOfService.
	DisruptionSignals []DisruptionSignal `json:"disruptionSignals,omitempty"`

	// CustomSignals are extra matchers you configure yourself (GKE/AKS/CA taints,
	// vendor annotations, etc.). They are ORed with DisruptionSignals.
	CustomSignals []CustomDisruptionSignal `json:"customSignals,omitempty"`
}

// EvictionGuardPolicyStatus is observed state for operators and other plugins.
type EvictionGuardPolicyStatus struct {
	// ObservedGeneration is the spec generation this status reflects.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// MatchedNodes is how many nodes currently pass NodeFilter.
	MatchedNodes int32 `json:"matchedNodes,omitempty"`

	// VulnerableNodes is how many matched nodes currently carry a disruption signal.
	VulnerableNodes int32 `json:"vulnerableNodes,omitempty"`

	// ActiveWindows is how many Open/Cooling/Held ProactiveWindows this policy owns.
	ActiveWindows int32 `json:"activeWindows,omitempty"`

	// DeferredWorkloads is how many at-risk opted-in Deployments are waiting
	// for a window slot because MaxConcurrentWindows is already reached.
	DeferredWorkloads int32 `json:"deferredWorkloads,omitempty"`

	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=egp,categories=eviction-guard
// +kubebuilder:printcolumn:name="Matched",type=integer,JSONPath=.status.matchedNodes
// +kubebuilder:printcolumn:name="Vulnerable",type=integer,JSONPath=.status.vulnerableNodes
// +kubebuilder:printcolumn:name="Windows",type=integer,JSONPath=.status.activeWindows
// +kubebuilder:printcolumn:name="Deferred",type=integer,JSONPath=.status.deferredWorkloads
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=.metadata.creationTimestamp

// EvictionGuardPolicy configures which nodes to watch and how to protect opted-in workloads.
// Multiple policies may coexist; each applies independently to the nodes its NodeFilter selects.
// Other Kubernetes plugins integrate by creating policies (and optionally watching ProactiveWindow).
type EvictionGuardPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EvictionGuardPolicySpec   `json:"spec,omitempty"`
	Status EvictionGuardPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EvictionGuardPolicyList contains a list of EvictionGuardPolicy.
type EvictionGuardPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EvictionGuardPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EvictionGuardPolicy{}, &EvictionGuardPolicyList{})
}

// DefaultSpareReplicas is used when Spec.SpareReplicas is unset.
const DefaultSpareReplicas int32 = 1

// DefaultMaxBuffer is used when Spec.MaxBuffer is unset.
const DefaultMaxBuffer int32 = 4

// DefaultMaxConcurrentWindows is used when Spec.MaxConcurrentWindows is unset.
// 0 on the spec means unlimited.
const DefaultMaxConcurrentWindows int32 = 8

func (p *EvictionGuardPolicy) SpareReplicasOrDefault() int32 {
	if p.Spec.SpareReplicas == nil {
		return DefaultSpareReplicas
	}
	return *p.Spec.SpareReplicas
}

func (p *EvictionGuardPolicy) MaxBufferOrDefault() int32 {
	if p.Spec.MaxBuffer == nil {
		return DefaultMaxBuffer
	}
	return *p.Spec.MaxBuffer
}

// MaxConcurrentWindowsOrDefault is the window cap. 0 means unlimited.
func (p *EvictionGuardPolicy) MaxConcurrentWindowsOrDefault() int32 {
	if p.Spec.MaxConcurrentWindows == nil {
		return DefaultMaxConcurrentWindows
	}
	return *p.Spec.MaxConcurrentWindows
}

func (p *EvictionGuardPolicy) RequirePDBOrDefault() bool {
	if p.Spec.RequirePDB == nil {
		return true
	}
	return *p.Spec.RequirePDB
}

func (p *EvictionGuardPolicy) DefaultBackendOrDefault() ScaleBackendType {
	if p.Spec.DefaultBackend == "" {
		return ScaleBackendDeployment
	}
	return p.Spec.DefaultBackend
}

func (p *EvictionGuardPolicy) BackendsOrDefault() []ScaleBackendType {
	out := []ScaleBackendType{p.DefaultBackendOrDefault()}
	out = append(out, p.Spec.AdditionalBackends...)
	return uniqueBackends(out)
}

func uniqueBackends(in []ScaleBackendType) []ScaleBackendType {
	seen := map[ScaleBackendType]bool{}
	var out []ScaleBackendType
	for _, b := range in {
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		out = append(out, b)
	}
	return out
}

func (p *EvictionGuardPolicy) ScaleBackAfterOrDefault() metav1.Duration {
	if p.Spec.ScaleBackAfter == nil {
		return metav1.Duration{Duration: DefaultScaleBackAfter}
	}
	return *p.Spec.ScaleBackAfter
}

// DefaultMaxWindow is used when Spec.MaxWindow is unset. 0 on the spec means unlimited.
const DefaultMaxWindow = 2 * time.Hour

// MaxWindowOrDefault is the Open-phase cap. 0 means unlimited.
func (p *EvictionGuardPolicy) MaxWindowOrDefault() time.Duration {
	if p.Spec.MaxWindow == nil {
		return DefaultMaxWindow
	}
	return p.Spec.MaxWindow.Duration
}
