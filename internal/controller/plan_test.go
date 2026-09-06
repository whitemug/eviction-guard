/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/backends"
)

func TestWrapBackendErrForbidden(t *testing.T) {
	tgt := backends.Target{
		ObjectKey:  client.ObjectKey{Namespace: "app", Name: "fireship"},
		APIVersion: "example.com/v1",
		Kind:       "WebApp",
	}
	err := wrapBackendErr(tgt, apierrors.NewForbidden(schema.GroupResource{Group: "example.com", Resource: "webapps"}, "fireship", errors.New("denied")))
	if !isMissingBackendRBAC(err) {
		t.Fatalf("want missing-RBAC wrapper, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing RBAC") || !strings.Contains(msg, "WebApp") || !strings.Contains(msg, "extraClusterRoleRules") {
		t.Fatalf("message=%q", msg)
	}
	if isMissingBackendRBAC(wrapBackendErr(tgt, errors.New("not found"))) {
		t.Fatal("non-Forbidden must not look like missing RBAC")
	}
}

func TestBuildPlanExternalOnly(t *testing.T) {
	policy := &egv1a1.EvictionGuardPolicy{
		Spec: egv1a1.EvictionGuardPolicySpec{
			SpareReplicas: ptr.To(int32(1)),
			MaxBuffer:     ptr.To(int32(4)),
			Backends: map[string]egv1a1.BackendCatalogEntry{
				"scaledobject": {
					APIVersion: "keda.sh/v1alpha1",
					Kind:       "ScaledObject",
				},
			},
		},
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "app",
			Annotations: map[string]string{
				egv1a1.ScaleBackendAnnotation: "scaledobject",
			},
		},
		Spec: appsv1.DeploymentSpec{Replicas: ptr.To(int32(3))},
	}
	c := fake.NewClientBuilder().Build()
	plan, err := buildPlan(context.Background(), c, policy, dep, 2, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.steps) != 0 {
		t.Fatalf("steps=%+v", plan.steps)
	}
	if plan.primaryBaseline != 3 {
		t.Fatalf("baseline=%d want 3", plan.primaryBaseline)
	}
	// baseline 3 + atRisk 2 with spareReplicas 1 → desired 5
	if plan.desired != 5 {
		t.Fatalf("desired=%d want 5", plan.desired)
	}
	if actions := plan.actions(nil); len(actions) != 0 {
		t.Fatalf("actions=%+v", actions)
	}
	plan2, err := buildPlan(context.Background(), c, policy, dep, 2, nil, 7)
	if err != nil {
		t.Fatal(err)
	}
	if plan2.primaryBaseline != 7 {
		t.Fatalf("sticky baseline=%d", plan2.primaryBaseline)
	}
}
