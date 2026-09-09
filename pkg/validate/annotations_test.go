/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package validate_test

import (
	"strings"
	"testing"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/validate"
)

func TestWorkloadAnnotationsOK(t *testing.T) {
	if err := validate.WorkloadAnnotations("default", nil); err != nil {
		t.Fatal(err)
	}
	ann := map[string]string{
		egv1a1.ScaleBackendAnnotation:   "deployment,hpa=web,webapp=fireship",
		egv1a1.ScaleBackAfterAnnotation: "20m",
		egv1a1.StampAnnotation:          "true",
		egv1a1.PolicyPinAnnotation:      "spot-workers",
	}
	if err := validate.WorkloadAnnotations("default", ann); err != nil {
		t.Fatal(err)
	}
}

func TestScaleBackendRequired(t *testing.T) {
	if err := validate.ScaleBackendRequired(nil); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("err=%v", err)
	}
	if err := validate.ScaleBackendRequired(map[string]string{}); err == nil {
		t.Fatal("expected required")
	}
	if err := validate.ScaleBackendRequired(map[string]string{egv1a1.ScaleBackendAnnotation: "deployment"}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkloadAnnotationsReject(t *testing.T) {
	cases := []struct {
		name string
		ann  map[string]string
		want string
	}{
		{
			name: "empty backend key",
			ann:  map[string]string{egv1a1.ScaleBackendAnnotation: "=foo"},
			want: "empty scale-backend key",
		},
		{
			name: "empty backend list",
			ann:  map[string]string{egv1a1.ScaleBackendAnnotation: ",,"},
			want: "empty scale-backend",
		},
		{
			name: "bad duration",
			ann:  map[string]string{egv1a1.ScaleBackAfterAnnotation: "soon"},
			want: "scale-back-after",
		},
		{
			name: "zero duration",
			ann:  map[string]string{egv1a1.ScaleBackAfterAnnotation: "0s"},
			want: "greater than 0",
		},
		{
			name: "bad stamp",
			ann:  map[string]string{egv1a1.StampAnnotation: "yes"},
			want: "true",
		},
		{
			name: "bad policy pin",
			ann:  map[string]string{egv1a1.PolicyPinAnnotation: "Not A Label"},
			want: "DNS-1123",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validate.WorkloadAnnotations("default", tc.ann)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want substring %q", err, tc.want)
			}
		})
	}
}

func catalogBackends() map[string]egv1a1.BackendCatalogEntry {
	return map[string]egv1a1.BackendCatalogEntry{
		"deployment": {
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Patches:    []egv1a1.BackendPatch{{Path: "spec.replicas"}},
		},
		"hpa": {
			APIVersion: "autoscaling/v1",
			Kind:       "HorizontalPodAutoscaler",
			Patches:    []egv1a1.BackendPatch{{Path: "spec.minReplicas"}},
		},
	}
}

func TestPolicyNamePattern(t *testing.T) {
	ok := &egv1a1.EvictionGuardPolicy{Spec: egv1a1.EvictionGuardPolicySpec{
		NodeFilter: egv1a1.NodeFilter{NamePattern: "ip-10-0-*"},
		Backends:   catalogBackends(),
	}}
	if err := validate.Policy(ok); err != nil {
		t.Fatal(err)
	}
	bad := &egv1a1.EvictionGuardPolicy{Spec: egv1a1.EvictionGuardPolicySpec{
		NodeFilter: egv1a1.NodeFilter{NamePattern: "["},
		Backends:   catalogBackends(),
	}}
	if err := validate.Policy(bad); err == nil {
		t.Fatal("expected invalid glob")
	}
	if err := validate.Policy(&egv1a1.EvictionGuardPolicy{}); err == nil || !strings.Contains(err.Error(), "backends") {
		t.Fatalf("expected backends required, got %v", err)
	}
}

func TestPolicyBackends(t *testing.T) {
	p := &egv1a1.EvictionGuardPolicy{Spec: egv1a1.EvictionGuardPolicySpec{
		Backends: map[string]egv1a1.BackendCatalogEntry{
			"deployment": {
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Patches:    []egv1a1.BackendPatch{{Path: "spec.replicas"}},
			},
		},
	}}
	if err := validate.Policy(p); err != nil {
		t.Fatal(err)
	}
	// Empty patches = external scaler entry (allowed).
	p.Spec.Backends["scaledobject"] = egv1a1.BackendCatalogEntry{
		APIVersion: "keda.sh/v1alpha1",
		Kind:       "ScaledObject",
	}
	if err := validate.Policy(p); err != nil {
		t.Fatal(err)
	}
	p.Spec.Backends["bad"] = egv1a1.BackendCatalogEntry{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Patches:    []egv1a1.BackendPatch{{}},
	}
	if err := validate.Policy(p); err == nil || !strings.Contains(err.Error(), "path or annotation") {
		t.Fatalf("err=%v", err)
	}
}
