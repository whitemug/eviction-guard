/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package backends_test

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/backends"
)

func TestParseBindings(t *testing.T) {
	got, err := backends.ParseBindings("deployment,hpa=fireship-hpa,webapp=other/fireship", "app", "fireship")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Key != "deployment" || got[0].Name != "fireship" || got[0].Namespace != "app" {
		t.Fatalf("deployment=%+v", got[0])
	}
	if got[1].Key != "hpa" || got[1].Name != "fireship-hpa" {
		t.Fatalf("hpa=%+v", got[1])
	}
	if got[2].Key != "webapp" || got[2].Namespace != "other" || got[2].Name != "fireship" {
		t.Fatalf("webapp=%+v", got[2])
	}
}

func TestBindingsForWorkload(t *testing.T) {
	policy := &egv1a1.EvictionGuardPolicy{
		Spec: egv1a1.EvictionGuardPolicySpec{
			Backends: map[string]egv1a1.BackendCatalogEntry{
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
			},
		},
	}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"}}

	if _, err := backends.BindingsForWorkload(dep, policy); err == nil {
		t.Fatal("expected error when scale-backend annotation is missing")
	} else if !strings.Contains(err.Error(), "required") {
		t.Fatalf("err=%v, want required", err)
	}

	dep.Annotations = map[string]string{egv1a1.ScaleBackendAnnotation: "deployment,hpa=web-hpa"}
	got, err := backends.BindingsForWorkload(dep, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "deployment" || got[1].Key != "hpa" || got[1].Name != "web-hpa" {
		t.Fatalf("annotated bindings=%+v", got)
	}
}

func TestResolveCatalog(t *testing.T) {
	policy := &egv1a1.EvictionGuardPolicy{
		Spec: egv1a1.EvictionGuardPolicySpec{
			Backends: map[string]egv1a1.BackendCatalogEntry{
				"deployment": {
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Patches:    []egv1a1.BackendPatch{{Path: "spec.replicas"}},
				},
				"hpa": {
					APIVersion: "autoscaling/v1",
					Kind:       "HorizontalPodAutoscaler",
					Patches: []egv1a1.BackendPatch{
						{Path: "spec.minReplicas"},
						{Path: "spec.maxReplicas"},
						{Annotation: "eviction-guard.io/active"},
					},
				},
				"webapp": {
					APIVersion: "example.com/v1",
					Kind:       "WebApp",
					Patches:    []egv1a1.BackendPatch{{Path: "spec.replicas"}, {Annotation: "eviction-guard.io/active"}},
				},
			},
		},
	}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "fireship", Namespace: "app"}}
	bindings := []backends.Binding{
		{Key: "deployment", Namespace: "app", Name: "fireship"},
		{Key: "hpa", Namespace: "app", Name: "fireship"},
		{Key: "webapp", Namespace: "app", Name: "fireship"},
	}
	steps, err := backends.ResolveCatalog(dep, policy, bindings)
	if err != nil {
		t.Fatal(err)
	}
	// deployment + hpa min + hpa max + webapp
	if len(steps) != 4 {
		t.Fatalf("len=%d want 4: %+v", len(steps), steps)
	}
	if steps[1].Target.FieldPath != "spec.minReplicas" || steps[2].Target.FieldPath != "spec.maxReplicas" {
		t.Fatalf("hpa paths=%q %q", steps[1].Target.FieldPath, steps[2].Target.FieldPath)
	}
	if len(steps[1].Annotations) != 1 || len(steps[2].Annotations) != 0 {
		t.Fatalf("catalog anns should attach to first path only: min=%v max=%v", steps[1].Annotations, steps[2].Annotations)
	}
	if steps[3].Target.Kind != "WebApp" || len(steps[3].Annotations) != 1 {
		t.Fatalf("webapp=%+v", steps[3])
	}
	if _, err := backends.ResolveCatalog(dep, policy, []backends.Binding{{Key: "missing", Namespace: "app", Name: "x"}}); err == nil {
		t.Fatal("expected unknown key error")
	}
}

func TestResolveCatalogExternalOnly(t *testing.T) {
	policy := &egv1a1.EvictionGuardPolicy{
		Spec: egv1a1.EvictionGuardPolicySpec{
			Backends: map[string]egv1a1.BackendCatalogEntry{
				"scaledobject": {
					APIVersion: "keda.sh/v1alpha1",
					Kind:       "ScaledObject",
					// Empty patches: declared for scale-backend / observability, not patched.
				},
			},
		},
	}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"}}
	bindings := []backends.Binding{{Key: "scaledobject", Namespace: "app", Name: "web"}}
	steps, err := backends.ResolveCatalog(dep, policy, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 0 {
		t.Fatalf("external-only should yield no patch steps, got %+v", steps)
	}
}

func TestResolveCatalogSkipsExternalAmongPaths(t *testing.T) {
	policy := &egv1a1.EvictionGuardPolicy{
		Spec: egv1a1.EvictionGuardPolicySpec{
			Backends: map[string]egv1a1.BackendCatalogEntry{
				"deployment": {
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Patches:    []egv1a1.BackendPatch{{Path: "spec.replicas"}},
				},
				"scaledobject": {
					APIVersion: "keda.sh/v1alpha1",
					Kind:       "ScaledObject",
				},
			},
		},
	}
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"}}
	bindings := []backends.Binding{
		{Key: "scaledobject", Namespace: "app", Name: "web"},
		{Key: "deployment", Namespace: "app", Name: "web"},
	}
	steps, err := backends.ResolveCatalog(dep, policy, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Key != "deployment" {
		t.Fatalf("want only deployment step, got %+v", steps)
	}
}
