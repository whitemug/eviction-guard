/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package signals_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

func TestKarpenterDisrupted(t *testing.T) {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	if signals.IsVulnerable(n, nil) {
		t.Fatal("clean node should not be vulnerable")
	}
	n.Spec.Taints = []corev1.Taint{{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule}}
	if !signals.IsVulnerable(n, nil) {
		t.Fatal("disrupted taint should be vulnerable")
	}
}

func TestDeleteRequestedAnnotation(t *testing.T) {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:        "n1",
		Annotations: map[string]string{signals.AnnotationDeleteAt: "2026-08-30T00:00:00Z"},
	}}
	if !signals.IsVulnerable(n, []egv1a1.DisruptionSignal{egv1a1.SignalKarpenterDeleteRequested}) {
		t.Fatal("expected delete-requested signal")
	}
}

func TestSignalAllowList(t *testing.T) {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec:       corev1.NodeSpec{Taints: []corev1.Taint{{Key: signals.TaintKarpenterDisrupted}}},
	}
	if signals.IsVulnerable(n, []egv1a1.DisruptionSignal{egv1a1.SignalOutOfService}) {
		t.Fatal("disrupted taint should be ignored when only OutOfService is enabled")
	}
}

func TestCustomTaintAndLabel(t *testing.T) {
	policy := &egv1a1.EvictionGuardPolicy{
		Spec: egv1a1.EvictionGuardPolicySpec{
			// Disable built-ins by asking for a signal that is not on the node,
			// then rely on customSignals.
			DisruptionSignals: []egv1a1.DisruptionSignal{egv1a1.SignalOutOfService},
			CustomSignals: []egv1a1.CustomDisruptionSignal{
				{Name: "GKEImpendingTermination", Taint: &egv1a1.TaintMatch{Key: "cloud.google.com/impending-node-termination"}},
				{Name: "GKEMaintenance", Label: &egv1a1.KeyValueMatch{Key: "cloud.google.com/active-node-maintenance", Value: "ONGOING"}},
			},
		},
	}
	clean := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	if signals.Vulnerable(clean, policy) {
		t.Fatal("clean node must not match custom signals")
	}
	tainted := clean.DeepCopy()
	tainted.Spec.Taints = []corev1.Taint{{Key: "cloud.google.com/impending-node-termination", Effect: corev1.TaintEffectNoSchedule}}
	if !signals.Vulnerable(tainted, policy) {
		t.Fatal("GKE taint should match customSignals")
	}
	labeled := clean.DeepCopy()
	labeled.Labels = map[string]string{"cloud.google.com/active-node-maintenance": "ONGOING"}
	if !signals.Vulnerable(labeled, policy) {
		t.Fatal("GKE label should match customSignals")
	}
}

func TestNodeCordonedIsDefault(t *testing.T) {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Spec:       corev1.NodeSpec{Unschedulable: true},
	}
	if !signals.IsVulnerable(n, nil) {
		t.Fatal("cordon should fire on default signals")
	}
	if signals.IsVulnerable(n, []egv1a1.DisruptionSignal{egv1a1.SignalKarpenterDisrupted}) {
		t.Fatal("cordon must not fire when NodeCordoned is omitted from the allow-list")
	}
}
