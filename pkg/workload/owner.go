/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package workload resolves primary workloads from pods (Deployment via ReplicaSet).
// Shared by the policy controller and eviction gate so ownership walks stay in sync.
package workload

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// OwnerDeployment walks Pod → ReplicaSet → Deployment (controller refs only).
// Returns (nil, nil) when the pod has no Deployment owner. NotFound on RS/Deployment
// is ignored (nil, nil); other Get errors are returned.
func OwnerDeployment(ctx context.Context, c client.Client, pod *corev1.Pod) (*appsv1.Deployment, error) {
	if pod == nil {
		return nil, nil
	}
	for _, o := range pod.OwnerReferences {
		if o.Kind != "ReplicaSet" || o.Controller == nil || !*o.Controller {
			continue
		}
		rs := &appsv1.ReplicaSet{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: o.Name}, rs); err != nil {
			return nil, client.IgnoreNotFound(err)
		}
		for _, oo := range rs.OwnerReferences {
			if oo.Kind != "Deployment" || oo.Controller == nil || !*oo.Controller {
				continue
			}
			dep := &appsv1.Deployment{}
			if err := c.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: oo.Name}, dep); err != nil {
				return nil, client.IgnoreNotFound(err)
			}
			return dep, nil
		}
	}
	return nil, nil
}
