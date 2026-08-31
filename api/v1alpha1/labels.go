/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package v1alpha1

// Well-known labels, annotations, and finalizers. Other controllers and
// plugins SHOULD use these constants rather than hard-coding the strings.
const (
	// EnabledLabel is the opt-in label for workloads. Value must be "true".
	EnabledLabel = "eviction-guard.io/enabled"

	// PolicyLabel is set on ProactiveWindow objects to identify the owning policy.
	PolicyLabel = "eviction-guard.io/policy"

	// WorkloadNameLabel / WorkloadNamespaceLabel identify the scaled workload.
	WorkloadNameLabel      = "eviction-guard.io/workload-name"
	WorkloadNamespaceLabel = "eviction-guard.io/workload-namespace"

	// ScaleBackendAnnotation selects one or more scaling backends (comma-separated).
	// Values: deployment, hpa-min, crd — e.g. "deployment,hpa-min,crd".
	ScaleBackendAnnotation = "eviction-guard.io/scale-backend"

	// ScaleTargetAnnotation is the object the crd backend patches
	// (group/version/namespaces/ns/kind/name). Also used as the HPA target
	// when hpa-target is unset and hpa-min is the only non-deployment backend.
	ScaleTargetAnnotation = "eviction-guard.io/scale-target"

	// HPATargetAnnotation is the HPA to raise minReplicas on (namespace/name).
	// Defaults to an HPA with the Deployment's namespace and name.
	HPATargetAnnotation = "eviction-guard.io/hpa-target"

	// CRDReplicasPathAnnotation is the JSON path of the replica field for the crd backend.
	// Defaults to "spec.replicas".
	CRDReplicasPathAnnotation = "eviction-guard.io/crd-replicas-path"

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

	// WindowFinalizer blocks ProactiveWindow deletion until capacity is restored.
	WindowFinalizer = "eviction-guard.io/scale-back"

	// PolicyFinalizer blocks EvictionGuardPolicy deletion until windows are cleaned up.
	PolicyFinalizer = "eviction-guard.io/cleanup"
)
