/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package signals detects predictable node disruption.
// Other plugins can Register additional Detector implementations (GKE maintenance,
// Azure eviction, vendor-specific taints) without changing the controllers.
package signals

import (
	corev1 "k8s.io/api/core/v1"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

const (
	TaintKarpenterDisrupted = "karpenter.sh/disrupted"
	TaintOutOfService       = "node.kubernetes.io/out-of-service"
	AnnotationDeleteAt      = "karpenter.sh/delete-requested-at"
	AnnotationSpotInterrupt = "aws.amazon.com/spot-interrupt"
	LabelSpotVolatile       = "spotinst.com/volatile"
)

// Detector reports whether a node is about to evict pods.
type Detector interface {
	Name() egv1a1.DisruptionSignal
	Vulnerable(node *corev1.Node) bool
}

var registry = map[egv1a1.DisruptionSignal]Detector{}

func init() {
	Register(karpenterDisrupted{})
	Register(karpenterDeleteRequested{})
	Register(outOfService{})
	Register(spotInterrupted{})
	Register(nodeCordoned{})
}

// Register adds or replaces a detector. Safe to call from plugin init().
func Register(d Detector) {
	registry[d.Name()] = d
}

// Lookup returns a registered detector.
func Lookup(name egv1a1.DisruptionSignal) (Detector, bool) {
	d, ok := registry[name]
	return d, ok
}

// DefaultSignals are enabled when a policy omits spec.disruptionSignals.
// NodeCordoned is included so kubectl drain / NTH / upgrade paths that only
// cordon still open a window; narrow with nodeFilter if cordons are noisy.
func DefaultSignals() []egv1a1.DisruptionSignal {
	return []egv1a1.DisruptionSignal{
		egv1a1.SignalKarpenterDisrupted,
		egv1a1.SignalKarpenterDeleteRequested,
		egv1a1.SignalOutOfService,
		egv1a1.SignalNodeCordoned,
	}
}

// IsVulnerable reports true if any of the requested signals fire on the node.
func IsVulnerable(node *corev1.Node, wanted []egv1a1.DisruptionSignal) bool {
	if len(wanted) == 0 {
		wanted = DefaultSignals()
	}
	for _, name := range wanted {
		d, ok := registry[name]
		if !ok {
			continue
		}
		if d.Vulnerable(node) {
			return true
		}
	}
	return false
}

// Reasons returns the signal names that currently fire (for events/status).
func Reasons(node *corev1.Node, wanted []egv1a1.DisruptionSignal) []egv1a1.DisruptionSignal {
	if len(wanted) == 0 {
		wanted = DefaultSignals()
	}
	var out []egv1a1.DisruptionSignal
	for _, name := range wanted {
		d, ok := registry[name]
		if !ok {
			continue
		}
		if d.Vulnerable(node) {
			out = append(out, name)
		}
	}
	return out
}

type karpenterDisrupted struct{}

func (karpenterDisrupted) Name() egv1a1.DisruptionSignal { return egv1a1.SignalKarpenterDisrupted }

func (karpenterDisrupted) Vulnerable(node *corev1.Node) bool {
	return hasTaint(node, TaintKarpenterDisrupted)
}

type karpenterDeleteRequested struct{}

func (karpenterDeleteRequested) Name() egv1a1.DisruptionSignal {
	return egv1a1.SignalKarpenterDeleteRequested
}

func (karpenterDeleteRequested) Vulnerable(node *corev1.Node) bool {
	if node.Annotations == nil {
		return false
	}
	return node.Annotations[AnnotationDeleteAt] != ""
}

type outOfService struct{}

func (outOfService) Name() egv1a1.DisruptionSignal { return egv1a1.SignalOutOfService }

func (outOfService) Vulnerable(node *corev1.Node) bool {
	return hasTaint(node, TaintOutOfService)
}

type spotInterrupted struct{}

func (spotInterrupted) Name() egv1a1.DisruptionSignal { return egv1a1.SignalSpotInterrupted }

func (spotInterrupted) Vulnerable(node *corev1.Node) bool {
	if hasTaint(node, TaintOutOfService) {
		return true
	}
	if node.Annotations != nil && node.Annotations[AnnotationSpotInterrupt] != "" {
		return true
	}
	if node.Labels != nil && node.Labels[LabelSpotVolatile] == "true" {
		return true
	}
	return false
}

type nodeCordoned struct{}

func (nodeCordoned) Name() egv1a1.DisruptionSignal { return egv1a1.SignalNodeCordoned }

func (nodeCordoned) Vulnerable(node *corev1.Node) bool {
	return node.Spec.Unschedulable
}

func hasTaint(node *corev1.Node, key string) bool {
	for _, t := range node.Spec.Taints {
		if t.Key == key {
			return true
		}
	}
	return false
}
