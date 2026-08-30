/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package backends

import (
	"context"
	"fmt"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

// HPAMinBackend raises HorizontalPodAutoscaler.spec.minReplicas.
type HPAMinBackend struct{}

func (*HPAMinBackend) Name() egv1a1.ScaleBackendType { return egv1a1.ScaleBackendHPAMin }

func (*HPAMinBackend) Current(ctx context.Context, c client.Client, t Target) (int32, error) {
	hpa, err := getHPA(ctx, c, t)
	if err != nil {
		return 0, err
	}
	if hpa.Spec.MinReplicas == nil {
		return 1, nil
	}
	return *hpa.Spec.MinReplicas, nil
}

func (*HPAMinBackend) ScaleUp(ctx context.Context, c client.Client, t Target, desired int32) error {
	return patchMinReplicas(ctx, c, t, desired, false)
}

func (*HPAMinBackend) ScaleDown(ctx context.Context, c client.Client, t Target, baseline int32) error {
	return patchMinReplicas(ctx, c, t, baseline, true)
}

// RestoreMinReplicas writes spec.minReplicas without clamping to
// status.currentReplicas. The window controller uses this after it has already
// decided load is not holding, so a following Deployment scale-down is not
// pinned by the spare HPA floor.
func RestoreMinReplicas(ctx context.Context, c client.Client, t Target, baseline int32) error {
	return patchMinReplicas(ctx, c, t, baseline, false)
}

func getHPA(ctx context.Context, c client.Client, t Target) (*autoscalingv1.HorizontalPodAutoscaler, error) {
	hpa := &autoscalingv1.HorizontalPodAutoscaler{}
	if err := c.Get(ctx, t.ObjectKey, hpa); err != nil {
		return nil, fmt.Errorf("get hpa %s: %w", t.ObjectKey, err)
	}
	return hpa, nil
}

func patchMinReplicas(ctx context.Context, c client.Client, t Target, desired int32, scaleDown bool) error {
	hpa, err := getHPA(ctx, c, t)
	if err != nil {
		return err
	}
	if scaleDown && hpa.Status.CurrentReplicas > desired {
		// Never lower minReplicas below what HPA is currently running — G4.
		desired = hpa.Status.CurrentReplicas
	}
	if hpa.Spec.MinReplicas != nil && *hpa.Spec.MinReplicas == desired {
		return nil
	}
	patch := client.MergeFrom(hpa.DeepCopy())
	hpa.Spec.MinReplicas = &desired
	if err := c.Patch(ctx, hpa, patch); err != nil {
		return fmt.Errorf("patch hpa %s minReplicas=%d: %w", t.ObjectKey, desired, err)
	}
	return nil
}
