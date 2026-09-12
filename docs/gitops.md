# GitOps (Argo CD / Flux)

Eviction Guard **patches live capacity** during an open window (`Deployment.spec.replicas`, `HorizontalPodAutoscaler.spec.minReplicas`, and any custom catalog paths). GitOps tools that force-apply desired state will **revert** those patches on the next sync unless capacity fields are excluded from reconcile.

EVG cannot lock fields against a hard sync. Configure GitOps to ignore the paths you listed on `eviction-guard.io/scale-backend`.

While a window is Open, Eviction Guard re-applies capacity if live values drop below the target (for example after a sync). That can thrash with GitOps; prefer ignore rules below. Longer sync intervals only reduce how often the fight happens.

If another autoscaler (KEDA) owns replicas instead of EVG patches, use empty-patch catalog entries plus `evg_at_risk_pods` / `evg_desired_replicas` — see [KEDA](keda.md).

## Argo CD

Add `ignoreDifferences` for every field Eviction Guard patches, and set sync to respect them:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: my-app
spec:
  syncPolicy:
    syncOptions:
      - RespectIgnoreDifferences=true
  ignoreDifferences:
    - group: apps
      kind: Deployment
      jsonPointers:
        - /spec/replicas
    - group: autoscaling
      kind: HorizontalPodAutoscaler
      jsonPointers:
        - /spec/minReplicas
        - /spec/maxReplicas  # if your policy patches max as well
```

Copy-paste stub: [`examples/argocd-ignore-differences.yaml`](../examples/argocd-ignore-differences.yaml).

### Tradeoff

With `RespectIgnoreDifferences=true`, a git commit that changes ignored fields (for example HPA `minReplicas`) **will not be applied** by Argo. Capacity for those paths becomes **runtime-owned** (HPA + Eviction Guard), same as teams that already ignore Deployment `replicas` when HPA manages them.

To change the floor from git: temporarily remove the ignore, sync, then restore the ignore — or change the live object and accept that git is not the source of truth for that field.

Without `RespectIgnoreDifferences`, `ignoreDifferences` mainly affects OutOfSync/diff; sync may still overwrite EVG’s patches.

## Flux

Flux has no single `ignoreDifferences` equivalent for arbitrary fields. Common approaches:

- Keep Deployment `replicas` out of git when HPA owns scaling; patch HPA `minReplicas` only with EVG and manage HPA outside the Kustomization, **or**
- Use a separate runtime overlay / `kustomize.toolkit.fluxcd.io/reconcile: disabled` on objects you accept as controller-owned (coarse), **or**
- Document that Flux-managed apps using EVG should not hard-sync HPA `minReplicas` / Deployment `replicas` from git during drains.

Prefer Argo’s field-level ignore when you need fine-grained coexistence.

## What Eviction Guard does not do

- Pause Argo Applications or Flux Kustomizations  
- Raise scaler ceilings (e.g. HPA `maxReplicas`) unless you **explicitly** list that path on the catalog entry (prefer leaving headroom; otherwise a rejected patch + `maxWindow` fail-opens — [How-to](howto.md#capacity-apply-failures-and-maxwindow-failover))  
- Prevent sync when GitOps is misconfigured — you will see scale thrash (EVG up, sync down)

Next: [Metrics](metrics.md) · [Configure](configure.md) · [How-to](howto.md).
