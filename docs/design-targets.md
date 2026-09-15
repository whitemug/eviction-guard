# Design note — Target abstraction (D2)

Status: **design inventory only** — no StatefulSet / non-Deployment primary workload implementation.
Primary workload remains Deployment for v1alpha1. Scale backends (HPA, KEDA, CRs) already fan out via the Policy catalog; see [design-scale-targets.md](design-scale-targets.md).

## Why

Accepted decision **D2**: plan membership / SpareReady / eviction gate beyond Deployment **before API freeze**. Today the product unit is “opted-in Deployment”; StatefulSet and other controllers are out of scope for shipping code but must not be frozen out of the CRD shapes.

## Deployment-hardcoded inventory (2026-09)

| Area | Call sites | What is Deployment-specific |
|---|---|---|
| Ownership walk | `pkg/workload.OwnerDeployment` (policy controller + `pkg/evictgate`); `internal/controller/spare.go` `podToWindows` | Pod → ReplicaSet → Deployment controller refs |
| Membership / opt-in | `workloadProtected`, `templateProtected`, `templateOptedIn` | `Deployment.spec.template.labels[protected]` |
| Policy ownership | `pkg/policyown` | Selectors / pin on `*appsv1.Deployment` |
| Backend bind | `pkg/backends.BindingsForWorkload` / `ResolveCatalog` | Annotation reader on Deployment |
| Window target | `WorkloadReference.Kind` usually `"Deployment"`; watches on `appsv1.Deployment` | SpareReady lists pods via Deployment selector |
| Admission | Deployment validating webhook path | Annotation / template checks for Deploy only |
| Catalog defaults | `pkg/backends` `defaultWhenUnset` | Special-case `Deployment` / HPA → 1 |

Capacity **patch** targets are already abstracted (catalog GVK + path). The hardcoding is the **primary** workload (membership + gate + SpareReady), not the scale backends.

## Planned Target sketch (pre-freeze)

Introduce a small internal interface (names illustrative):

```text
type Target interface {
    // Resolve from a pod (or Window.spec.target) to the primary object.
    GVK() schema.GroupVersionKind
    NamespacedName() types.NamespacedName
    // Pod selector / membership for SpareReady and eviction sequencing.
    PodSelector() labels.Selector
    TemplateProtected() bool
    // Annotations for scale-backend / policy-pin (metadata on the primary).
    Annotations() map[string]string
}
```

| Concern | Deployment today | Future StatefulSet (sketch) |
|---|---|---|
| Owner walk | RS → Deployment | Pod → StatefulSet (no RS) |
| Opt-in | template labels | template labels (same label) |
| SpareReady | Ready pods matching Deploy selector off vuln nodes | Same, but selector from STS |
| Scale bind | `scale-backend` on Deploy | Same annotation on STS **or** only via backends |
| Window.Target.Kind | `Deployment` | `StatefulSet` (API already has Kind string) |

**Non-goals for first cut:** DaemonSet, Job/CronJob, bare ReplicaSet, OpenKruise CloneSet (may reuse Target later).

**API freeze checklist:** keep `WorkloadReference.Kind` open; avoid hard-coding `"Deployment"` in CRD validation; prefer Target helpers over new Deployment-typed fields on Policy/Window.

## Implementation order (when scheduled)

1. Finish sharing ownership via `pkg/workload` (started: OwnerDeployment).
2. Extract Target behind Deployment adapter; swap call sites.
3. Add StatefulSet adapter + webhook + e2e only after adapter is stable.

Until then: operators use Deployment as the protected primary; scale StatefulSet-adjacent capacity only through catalog backends if needed.
