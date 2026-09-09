/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package policyown_test

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/policyown"
)

func policy(name string, workloadLabels map[string]string) *egv1a1.EvictionGuardPolicy {
	p := &egv1a1.EvictionGuardPolicy{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if workloadLabels != nil {
		p.Spec.WorkloadSelector = &metav1.LabelSelector{MatchLabels: workloadLabels}
	}
	return p
}

func dep(name string, labels, annotations map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "app",
			Labels:      labels,
			Annotations: annotations,
		},
	}
}

func TestFirstByName(t *testing.T) {
	web := map[string]string{"profile": "web"}
	d := dep("api", web, nil)
	a := policy("upgrade-pool", web)
	b := policy("spot-workers", web)
	got := policyown.FirstByName(d, nil, []*egv1a1.EvictionGuardPolicy{a, b})
	if got == nil || got.Name != "spot-workers" {
		t.Fatalf("got %v, want spot-workers (lexicographic first)", got)
	}
}

func TestOwnsPinIgnoresSelectors(t *testing.T) {
	d := dep("api", map[string]string{"profile": "batch"}, map[string]string{
		egv1a1.PolicyPinAnnotation: "web-aggressive",
	})
	web := policy("web-aggressive", map[string]string{"profile": "web"})
	batch := policy("batch-lazy", map[string]string{"profile": "batch"})
	all := []*egv1a1.EvictionGuardPolicy{web, batch}

	if !policyown.Owns(web, d, nil, all) {
		t.Fatal("pinned policy should own even when workloadSelector does not match")
	}
	if policyown.Owns(batch, d, nil, all) {
		t.Fatal("non-pinned policy must not own when pin is set")
	}
}

func TestOwnsPinMissingBlocksOthers(t *testing.T) {
	d := dep("api", nil, map[string]string{egv1a1.PolicyPinAnnotation: "missing"})
	p := policy("spot-workers", nil)
	if policyown.Owns(p, d, nil, []*egv1a1.EvictionGuardPolicy{p}) {
		t.Fatal("no policy should own when pin names a missing policy")
	}
	if got := policyown.ResolveOwner(d, nil, []*egv1a1.EvictionGuardPolicy{p}); got != nil {
		t.Fatalf("ResolveOwner=%v, want nil", got)
	}
}

func TestOwnsWithoutPinUsesFirstByName(t *testing.T) {
	d := dep("api", nil, nil)
	a := policy("zeta", nil)
	b := policy("alpha", nil)
	all := []*egv1a1.EvictionGuardPolicy{a, b}
	if !policyown.Owns(b, d, labels.Set{}, all) {
		t.Fatal("alpha should own")
	}
	if policyown.Owns(a, d, labels.Set{}, all) {
		t.Fatal("zeta should not own")
	}
}

func TestResolveOwnerPin(t *testing.T) {
	d := dep("api", nil, map[string]string{egv1a1.PolicyPinAnnotation: "zeta"})
	a := policy("zeta", nil)
	b := policy("alpha", nil)
	got := policyown.ResolveOwner(d, nil, []*egv1a1.EvictionGuardPolicy{a, b})
	if got == nil || got.Name != "zeta" {
		t.Fatalf("got %v, want zeta", got)
	}
}
