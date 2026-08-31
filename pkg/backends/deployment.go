/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package backends

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

// DeploymentBackend patches Deployment.spec.replicas.
type DeploymentBackend struct{}

func (*DeploymentBackend) Name() egv1a1.ScaleBackendType { return egv1a1.ScaleBackendDeployment }

func (*DeploymentBackend) Current(ctx context.Context, c client.Client, t Target) (int32, error) {
	dep, err := getDeployment(ctx, c, t)
	if err != nil {
		return 0, err
	}
	if dep.Spec.Replicas == nil {
		return 1, nil
	}
	return *dep.Spec.Replicas, nil
}

func (*DeploymentBackend) ScaleUp(ctx context.Context, c client.Client, t Target, desired int32) error {
	return patchReplicas(ctx, c, t, desired)
}

func (*DeploymentBackend) ScaleDown(ctx context.Context, c client.Client, t Target, baseline int32) error {
	return patchReplicas(ctx, c, t, baseline)
}

func getDeployment(ctx context.Context, c client.Client, t Target) (*appsv1.Deployment, error) {
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, t.ObjectKey, dep); err != nil {
		return nil, fmt.Errorf("get deployment %s: %w", t.ObjectKey, err)
	}
	return dep, nil
}

func patchReplicas(ctx context.Context, c client.Client, t Target, desired int32) error {
	dep, err := getDeployment(ctx, c, t)
	if err != nil {
		return err
	}
	if dep.Spec.Replicas != nil && *dep.Spec.Replicas == desired {
		return nil
	}
	patch := client.MergeFrom(dep.DeepCopy())
	dep.Spec.Replicas = &desired
	if err := c.Patch(ctx, dep, patch); err != nil {
		return fmt.Errorf("patch deployment %s replicas=%d: %w", t.ObjectKey, desired, err)
	}
	return nil
}
