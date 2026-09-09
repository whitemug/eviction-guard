/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package v1alpha1

// Well-known labels, annotations, and finalizers. Other controllers and
// plugins SHOULD use these constants rather than hard-coding the strings.
const (
	// ProtectedLabel on the pod template opts the workload in. Value must be
	// "true". Voluntary eviction is gated by the pods/eviction validating
	// webhook until spare capacity is Ready — this label alone does not block.
	ProtectedLabel = "eviction-guard.io/protected"

	// PolicyLabel is set on EvictionGuardWindow objects to identify the owning policy.
	PolicyLabel = "eviction-guard.io/policy"

	// PolicyPinAnnotation on a Deployment forces that named EvictionGuardPolicy to own
	// the workload. When set, ownership ignores namespaceSelector / workloadSelector
	// (nodeFilter and disruption signals still apply). If the named policy does not
	// exist, no other policy may scale or gate this workload.
	PolicyPinAnnotation = "eviction-guard.io/policy-pin"

	// WorkloadNameLabel / WorkloadNamespaceLabel identify the scaled workload.
	WorkloadNameLabel      = "eviction-guard.io/workload-name"
	WorkloadNamespaceLabel = "eviction-guard.io/workload-namespace"

	// ScaleBackendAnnotation selects scale targets as catalog keys (comma-separated),
	// with optional name overrides (e.g. "deployment,hpa=my-hpa,webapp=fireship").
	// Required on protected Deployments: without it, Eviction Guard does not scale.
	// Keys must exist on the owning policy's spec.backends. Token order is patch
	// order for entries with integer paths; the first patched path is the SpareReady
	// primary. Empty-patch catalog entries are external (declared only).
	ScaleBackendAnnotation = "eviction-guard.io/scale-backend"

	// ScaleBackAfterAnnotation overrides policy spec.scaleBackAfter for one workload
	// (Go duration, e.g. "30m", "1h").
	ScaleBackAfterAnnotation = "eviction-guard.io/scale-back-after"

	// StampAnnotation enables ("true") or disables ("false") writing visibility
	// annotations onto every scaled object (Deployment, HPA, and/or CR).
	// Overrides policy spec.stamp.enabled.
	StampAnnotation = "eviction-guard.io/stamp"

	// Optional overrides of the annotation *keys* written onto scaled objects.
	StampScaledToKeyAnnotation    = "eviction-guard.io/stamp-scaled-to"
	StampBaselineKeyAnnotation    = "eviction-guard.io/stamp-baseline"
	StampActiveKeyAnnotation      = "eviction-guard.io/stamp-active"
	StampWindowUntilKeyAnnotation = "eviction-guard.io/stamp-window-until"

	// Default keys written onto scaled objects when stamping is enabled.
	StampScaledToKey    = "eviction-guard.io/scaled-to"
	StampBaselineKey    = "eviction-guard.io/baseline"
	StampActiveKey      = "eviction-guard.io/active"
	StampWindowUntilKey = "eviction-guard.io/window-until"

	// WindowFinalizer blocks EvictionGuardWindow deletion until capacity is restored.
	WindowFinalizer = "eviction-guard.io/scale-back"

	// PolicyFinalizer blocks EvictionGuardPolicy deletion until windows are cleaned up.
	PolicyFinalizer = "eviction-guard.io/cleanup"
)
