/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package filters_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/filters"
)

func node(name string, labels map[string]string, taints ...corev1.Taint) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       corev1.NodeSpec{Taints: taints},
	}
}

func mustMatch(t *testing.T, f filters.Filter, n *corev1.Node, want bool) {
	t.Helper()
	got, err := f.Matches(n)
	if err != nil {
		t.Fatalf("Matches: %v", err)
	}
	if got != want {
		t.Fatalf("Matches(%s)=%v, want %v", n.Name, got, want)
	}
}

func TestEmptyFilterMatchesAll(t *testing.T) {
	f, err := filters.FromSpec(egv1a1.NodeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	mustMatch(t, f, node("a", nil), true)
}

func TestLabelSelectorAndExclude(t *testing.T) {
	f, err := filters.FromSpec(egv1a1.NodeFilter{
		LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"pool": "app"}},
		ExcludeLabelSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"eviction-guard.io/ignore": "true"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustMatch(t, f, node("in", map[string]string{"pool": "app"}), true)
	mustMatch(t, f, node("out", map[string]string{"pool": "system"}), false)
	mustMatch(t, f, node("ignored", map[string]string{"pool": "app", "eviction-guard.io/ignore": "true"}), false)
}

func TestNamesPatternZoneCapacity(t *testing.T) {
	f, err := filters.FromSpec(egv1a1.NodeFilter{
		Names:         []string{"ip-10-0-1-5", "ip-10-0-1-6"},
		NamePattern:   "ip-10-0-*",
		Zones:         []string{"us-east-1a"},
		CapacityTypes: []string{"spot"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ok := node("ip-10-0-1-5", map[string]string{
		"topology.kubernetes.io/zone": "us-east-1a",
		"karpenter.sh/capacity-type":  "spot",
	})
	mustMatch(t, f, ok, true)
	mustMatch(t, f, node("ip-10-0-1-5", map[string]string{
		"topology.kubernetes.io/zone": "us-east-1b",
		"karpenter.sh/capacity-type":  "spot",
	}), false)
	mustMatch(t, f, node("ip-10-0-9-9", map[string]string{
		"topology.kubernetes.io/zone": "us-east-1a",
		"karpenter.sh/capacity-type":  "spot",
	}), false)
}

func TestTaintSelector(t *testing.T) {
	f, err := filters.FromSpec(egv1a1.NodeFilter{
		TaintSelector: []egv1a1.TaintMatch{{Key: "dedicated", Value: "gpu"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustMatch(t, f, node("gpu", nil, corev1.Taint{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule}), true)
	mustMatch(t, f, node("cpu", nil), false)
}
