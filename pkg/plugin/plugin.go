/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package plugin is the compiled-in extension surface for other Kubernetes
// solutions that want to run inside the Eviction Guard process.
//
// Capacity targets are declared on EvictionGuardPolicy.spec.backends (no Go
// plugin required). Use this package for custom disruption Detectors compiled
// into the binary. Node coverage uses EvictionGuardPolicy.spec.nodeFilter
// (and customSignals); there is no named Go filter registry.
package plugin

import (
	"github.com/whitemug/eviction-guard/pkg/signals"
)

// RegisterSignal adds a disruption detector (see pkg/signals).
func RegisterSignal(d signals.Detector) { signals.Register(d) }
