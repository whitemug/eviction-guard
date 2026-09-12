# Metrics

Prometheus metrics on the manager (`:8080` by default). Names are part of the observability contract.

| Metric | Labels | Meaning |
|---|---|---|
| `evg_matched_nodes` | `policy` | Nodes matching `nodeFilter` |
| `evg_vulnerable_nodes` | `policy` | Matched nodes with a disruption signal |
| `evg_at_risk_pods` | `policy`, `namespace`, `workload` | Protected pods on vulnerable nodes |
| `evg_desired_replicas` | `policy`, `namespace`, `workload` | Capacity target (baseline + spare/at-risk, `maxBuffer` cap) |
| `evg_current_spare` | `policy`, `namespace`, `workload` | `desired - baseline` while EVG is holding capacity |
| `evg_spare_not_ready` | `policy`, `namespace`, `workload` | `1` while a window is open and spare is not Ready off dying nodes |
| `evg_capacity_apply_error` | `policy`, `namespace`, `workload` | `1` when the last capacity patch failed (admission/RBAC/other); spare may not land until fixed or `maxWindow` |
| `evg_deferred_workloads` | `policy` | Workloads waiting on `maxConcurrentWindows` |
| `evg_scale_actions_total` | `policy`, `direction`, `backend`, `result` | Scale-up / scale-back attempts |
| `evg_max_window_exceeded_total` | `policy`, `namespace`, `workload` | Windows force-cooled by `maxWindow` |
| `evg_eviction_decisions_total` | `decision` | Webhook allow/deny |

## External autoscalers (KEDA)

Prefer the full recipes in [KEDA](keda.md). Short version: `evg_at_risk_pods` and `evg_desired_replicas` are Prometheus inputs when another controller owns replica count. Catalog entries may omit path patches (external) so EVG opens a Window without capacity writes.

Restrict the metrics bind address on multi-tenant clusters ([SECURITY.md](../SECURITY.md)).

Next: [KEDA](keda.md) · [GitOps](gitops.md) · [Configure](configure.md) · [How-to](howto.md).
