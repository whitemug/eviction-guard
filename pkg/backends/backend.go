/*
Copyright 2026 Whitemug.

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

// PatchIntegers writes multiple integer fields on one object in a single merge
// patch. paths is an ordered list of field paths; values[path] is the desired
// integer. Use this when several catalog paths target the same object (e.g. HPA
// min+max) so admission validation sees a consistent object.
func PatchIntegers(ctx context.Context, c client.Client, base Target, paths []string, values map[string]int32) error {
	return Field.PatchIntegers(ctx, c, base, paths, values)
}
