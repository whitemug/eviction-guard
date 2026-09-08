/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package backends is the capacity-mutation API: catalog bindings and a generic
// integer field patcher. Custom capacity targets are declared on
// EvictionGuardPolicy.spec.backends (apiVersion/kind/path), not via Go plugins.
package backends

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Target identifies the object to mutate.
type Target struct {
	client.ObjectKey
	APIVersion string
	Kind       string
	// FieldPath is the integer capacity field (default spec.replicas).
	FieldPath string
}

// Field is the sole capacity mutator used by the controllers (unstructured merge-patch).
var Field = &fieldBackend{}

// Current reads the integer field on t.
func Current(ctx context.Context, c client.Client, t Target) (int32, error) {
	return Field.Current(ctx, c, t)
}

// ScaleUp sets the integer field to desired when it is lower.
func ScaleUp(ctx context.Context, c client.Client, t Target, desired int32) error {
	return Field.ScaleUp(ctx, c, t, desired)
}

// ScaleDown sets the integer field to baseline.
func ScaleDown(ctx context.Context, c client.Client, t Target, baseline int32) error {
	return Field.ScaleDown(ctx, c, t, baseline)
}
