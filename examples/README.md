# Examples

Install the operator, then apply a policy and a workload:

```bash
kubectl apply -f examples/policy-spot.yaml
kubectl apply -f examples/workload.yaml
```

`policy-spot.yaml` matches Spot nodes. `workload.yaml` opts in one Deployment.

The other files in this directory are optional: cordon and pool filters, HPA, KEDA, and Argo CD. The guides link the file you need.
