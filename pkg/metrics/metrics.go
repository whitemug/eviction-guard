/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package metrics holds Prometheus instruments other plugins can reuse.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	VulnerableNodes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "evg_vulnerable_nodes",
		Help: "Nodes currently matching a policy filter and carrying a disruption signal.",
	}, []string{"policy"})

	MatchedNodes = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "evg_matched_nodes",
		Help: "Nodes currently matching a policy NodeFilter.",
	}, []string{"policy"})

	CurrentSpare = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "evg_current_spare",
		Help: "Extra replicas currently held open by Eviction Guard for a workload.",
	}, []string{"policy", "namespace", "workload"})

	ScaleActions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "evg_scale_actions_total",
		Help: "Scale-up and scale-back actions by backend and result.",
	}, []string{"policy", "direction", "backend", "result"})

	SpareNotReady = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "evg_spare_not_ready",
		Help: "1 when a window is open but spare pods are not yet Ready off the vulnerable node(s).",
	}, []string{"policy", "namespace", "workload"})

	DeferredWorkloads = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "evg_deferred_workloads",
		Help: "At-risk opted-in Deployments waiting for a maxConcurrentWindows slot.",
	}, []string{"policy"})

	MaxWindowExceeded = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "evg_max_window_exceeded_total",
		Help: "Windows that force-cooled because maxWindow elapsed while still Open.",
	}, []string{"policy", "namespace", "workload"})
)

func init() {
	metrics.Registry.MustRegister(VulnerableNodes, MatchedNodes, CurrentSpare, ScaleActions, SpareNotReady, DeferredWorkloads, MaxWindowExceeded)
}
