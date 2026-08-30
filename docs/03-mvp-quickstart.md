# MVP Quickstart (CronJob + Script)

A scriptable proof-of-concept. This MVP runs a CronJob that performs the proactive scale-up and HPA-aware scale-back described in `02-solution-design.md`.

> **Intended for evaluation in a test cluster under synthetic load.** Not for production without the Controller extension (Section 9).

## Prerequisites

- A Kubernetes cluster with Karpenter-ish node rotation signals, or the ability to simulate node taints.
- `kubectl` access.
- A Deployment that you opt in to Eviction Guard with (optionally choosing a scaling backend, §7):
  ```yaml
  metadata:
    labels:
      kubernetes.io/eviction-guard: "enabled"        # opt-in
    annotations:
      eviction-guard.io/scale-backend: "deployment"  # deployment | hpa-min | crd
      eviction-guard.io/scale-target: "app/web"      # object to act on
  ```
  and a Pod Disruption Budget:
  ```yaml
  apiVersion: policy/v1
  kind: PodDisruptionBudget
  metadata:
    name: web-pdb
  spec:
    minAvailable: 2
    selector:
      matchLabels:
        app: web
  ```

## Deploy

1. Create the `eviction-guard` namespace, then the ServiceAccount and RBAC (list/watch nodes+pods, patch Deployments):
   ```bash
   kubectl create namespace eviction-guard
   kubectl apply -f deploy/rbac.yaml
   ```
2. Create the CronJob:
   ```bash
   kubectl apply -f deploy/evg-cronjob.yaml
   ```

## Verify

- Watch vulnerable-node detection and scale actions in logs:
  ```bash
  kubectl logs -n eviction-guard job/evg-<id> -f
  ```
- Confirm the opted-in Deployment replica count rises before eviction, and returns to baseline after the cooldown window.

## Simulating a disruption

To exercise the mechanism without real cluster rotation, add the vulnerability markers to a node running your opted-in Deployment's pods:

```bash
kubectl taint node <node> karpenter.sh/disrupted=:NoSchedule
# or, to simulate a termination notice:
kubectl taint node <node> node.kubernetes.io/out-of-service=:NoSchedule
```

Run the CronJob and observe the scale-up; remove the taint, wait past `scaleBackAfter`, and observe the scale-back.

> The taints/annotations actually used are read from the *cluster's real* Karpenter or cloud events in production — the above is only for local validation.
