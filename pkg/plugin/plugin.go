/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package plugin is the compiled-in extension surface for other Kubernetes
// solutions that want to run inside the Eviction Guard process.
//
// Kubernetes-native integration (no Go import required) is done by applying
// EvictionGuardPolicy / watching ProactiveWindow. Use this package only when
// you need a custom Backend, Detector, or Filter compiled into the binary.
package plugin

import (
	"github.com/whitemug/eviction-guard/pkg/backends"
	"github.com/whitemug/eviction-guard/pkg/filters"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

// RegisterBackend adds a scaling backend (see pkg/backends).
func RegisterBackend(b backends.Backend) { backends.Register(b) }

// RegisterSignal adds a disruption detector (see pkg/signals).
func RegisterSignal(d signals.Detector) { signals.Register(d) }

// RegisterFilter adds a named node-filter factory (see pkg/filters).
func RegisterFilter(name string, f filters.Factory) { filters.Register(name, f) }
