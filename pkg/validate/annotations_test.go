/*
Copyright 2026 Vikas Verma.

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
		egv1a1.ScaleBackendAnnotation:    "deployment,hpa-min,crd",
		egv1a1.HPATargetAnnotation:       "default/web",
		egv1a1.ScaleTargetAnnotation:     "example.com/v1/namespaces/default/Widget/web",
		egv1a1.ScaleBackAfterAnnotation:  "20m",
		egv1a1.StampAnnotation:           "true",
		egv1a1.CRDReplicasPathAnnotation: "spec.replicas",
	}
	if err := validate.WorkloadAnnotations("default", ann); err != nil {
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
			name: "unknown backend",
			ann:  map[string]string{egv1a1.ScaleBackendAnnotation: "nope"},
			want: "unknown scale backend",
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
			name: "bad scale-target",
			ann:  map[string]string{egv1a1.ScaleTargetAnnotation: "a/b/c"},
			want: "scale-target",
		},
		{
			name: "crd without target",
			ann:  map[string]string{egv1a1.ScaleBackendAnnotation: "crd"},
			want: "requires annotation",
		},
		{
			name: "crd with short target",
			ann: map[string]string{
				egv1a1.ScaleBackendAnnotation: "crd",
				egv1a1.ScaleTargetAnnotation:  "default/web",
			},
			want: "group/version/namespaces",
		},
		{
			name: "bad stamp",
			ann:  map[string]string{egv1a1.StampAnnotation: "yes"},
			want: "true",
		},
		{
			name: "bad hpa-target",
			ann:  map[string]string{egv1a1.HPATargetAnnotation: "too/many/parts/here"},
			want: "hpa-target",
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

func TestPolicyNamePattern(t *testing.T) {
	ok := &egv1a1.EvictionGuardPolicy{Spec: egv1a1.EvictionGuardPolicySpec{
		NodeFilter: egv1a1.NodeFilter{NamePattern: "ip-10-0-*"},
	}}
	if err := validate.Policy(ok); err != nil {
		t.Fatal(err)
	}
	bad := &egv1a1.EvictionGuardPolicy{Spec: egv1a1.EvictionGuardPolicySpec{
		NodeFilter: egv1a1.NodeFilter{NamePattern: "["},
	}}
	if err := validate.Policy(bad); err == nil {
		t.Fatal("expected invalid glob")
	}
	if err := validate.Policy(&egv1a1.EvictionGuardPolicy{}); err != nil {
		t.Fatal(err)
	}
}
