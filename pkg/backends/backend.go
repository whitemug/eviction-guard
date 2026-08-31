/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package backends is the pluggable capacity-mutation API.
// Other operators implement Backend and Register it (or use the built-in
// "crd" backend to increment a field on their own custom resource).
package backends

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

// Target identifies the object a backend should mutate.
type Target struct {
	client.ObjectKey
	APIVersion string
	Kind       string
	// FieldPath is used by the crd backend (default spec.replicas).
	FieldPath string
}

// Backend grows or shrinks capacity for a workload.
type Backend interface {
	Name() egv1a1.ScaleBackendType
	// Current returns the live capacity value (replicas or minReplicas).
	Current(ctx context.Context, c client.Client, t Target) (int32, error)
	ScaleUp(ctx context.Context, c client.Client, t Target, desired int32) error
	ScaleDown(ctx context.Context, c client.Client, t Target, baseline int32) error
}

var registry = map[egv1a1.ScaleBackendType]Backend{}

func init() {
	Register(&DeploymentBackend{})
	Register(&HPAMinBackend{})
	Register(&CRDBackend{})
}

// Register adds or replaces a backend. Safe to call from plugin init().
func Register(b Backend) {
	registry[b.Name()] = b
}

// Get returns a backend by type.
func Get(name egv1a1.ScaleBackendType) (Backend, error) {
	if name == "" {
		name = egv1a1.ScaleBackendDeployment
	}
	b, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown scale backend %q", name)
	}
	return b, nil
}
