/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package backends

import (
	"context"
	"fmt"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RestoreMinReplicas writes HPA spec.minReplicas without clamping to
// status.currentReplicas. Used when the window controller has already decided
// load is not holding (coordinated deploy+HPA restore).
func RestoreMinReplicas(ctx context.Context, c client.Client, t Target, baseline int32) error {
	return patchMinReplicas(ctx, c, t, baseline, false)
}

// ScaleDownMinReplicas lowers HPA minReplicas, never below status.currentReplicas (G4).
func ScaleDownMinReplicas(ctx context.Context, c client.Client, t Target, baseline int32) error {
	return patchMinReplicas(ctx, c, t, baseline, true)
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
