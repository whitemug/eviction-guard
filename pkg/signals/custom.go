/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package signals

import (
	corev1 "k8s.io/api/core/v1"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

// Vulnerable reports whether the node matches the policy's built-in and/or custom signals.
func Vulnerable(node *corev1.Node, policy *egv1a1.EvictionGuardPolicy) bool {
	if policy == nil {
		return IsVulnerable(node, nil)
	}
	if IsVulnerable(node, policy.Spec.DisruptionSignals) {
		return true
	}
	return MatchCustom(node, policy.Spec.CustomSignals)
}

// MatchCustom returns true if any configured custom signal matches the node.
func MatchCustom(node *corev1.Node, signals []egv1a1.CustomDisruptionSignal) bool {
	for i := range signals {
		if customMatches(node, &signals[i]) {
			return true
		}
	}
	return false
}

func customMatches(node *corev1.Node, sig *egv1a1.CustomDisruptionSignal) bool {
	if sig.Taint != nil && taintMatch(node, *sig.Taint) {
		return true
	}
	if sig.Annotation != nil && kvMatch(node.Annotations, sig.Annotation) {
		return true
	}
	if sig.Label != nil && kvMatch(node.Labels, sig.Label) {
		return true
	}
	return false
}

func taintMatch(node *corev1.Node, want egv1a1.TaintMatch) bool {
	for _, t := range node.Spec.Taints {
		if t.Key != want.Key {
			continue
		}
		if want.Value != "" && t.Value != want.Value {
			continue
		}
		if want.Effect != "" && t.Effect != want.Effect {
			continue
		}
		return true
	}
	return false
}

func kvMatch(m map[string]string, want *egv1a1.KeyValueMatch) bool {
	if want == nil || m == nil {
		return false
	}
	v, ok := m[want.Key]
	if !ok {
		return false
	}
	if want.Value != "" && v != want.Value {
		return false
	}
	return true
}
