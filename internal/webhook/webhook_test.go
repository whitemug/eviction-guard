/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

func decoder(t *testing.T) admission.Decoder {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := egv1a1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return admission.NewDecoder(scheme)
}

func TestDeploymentWebhook(t *testing.T) {
	v := &deploymentValidator{decoder: decoder(t)}
	ok := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "default",
			Annotations: map[string]string{
				egv1a1.ScaleBackendAnnotation: "deployment",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
					egv1a1.ProtectedLabel: "true",
				}},
			},
		},
	}
	resp := v.Handle(context.Background(), mustReq(t, ok))
	if !resp.Allowed {
		t.Fatalf("allowed=false: %s", resp.Result.Message)
	}

	missing := ok.DeepCopy()
	missing.Annotations = nil
	resp = v.Handle(context.Background(), mustReq(t, missing))
	if resp.Allowed {
		t.Fatal("expected deny when protected without scale-backend")
	}

	bad := ok.DeepCopy()
	bad.Annotations[egv1a1.ScaleBackendAnnotation] = "=missing-key"
	resp = v.Handle(context.Background(), mustReq(t, bad))
	if resp.Allowed {
		t.Fatal("expected deny")
	}
}

func TestPolicyWebhook(t *testing.T) {
	v := &policyValidator{decoder: decoder(t)}
	ok := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot"},
		Spec: egv1a1.EvictionGuardPolicySpec{
			NodeFilter: egv1a1.NodeFilter{NamePattern: "spot-*"},
			Backends: map[string]egv1a1.BackendCatalogEntry{
				"deployment": {
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Patches:    []egv1a1.BackendPatch{{Path: "spec.replicas"}},
				},
			},
		},
	}
	resp := v.Handle(context.Background(), mustReq(t, ok))
	if !resp.Allowed {
		t.Fatalf("allowed=false: %s", resp.Result.Message)
	}
	bad := ok.DeepCopy()
	bad.Spec.NodeFilter.NamePattern = "["
	resp = v.Handle(context.Background(), mustReq(t, bad))
	if resp.Allowed {
		t.Fatal("expected deny")
	}
}

func TestEvictionWebhookPodNotFoundAllows(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	v := &evictionValidator{client: c, decoder: decoder(t)}
	resp := v.Handle(context.Background(), evictionReq(t, "app", "gone"))
	if !resp.Allowed {
		t.Fatalf("NotFound must allow, got %+v", resp)
	}
}

func TestEvictionWebhookGetErrorDoesNotFailOpen(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	base := fake.NewClientBuilder().WithScheme(scheme).Build()
	c := &errGetClient{Client: base, err: apierrors.NewInternalError(errors.New("apiserver blip"))}
	v := &evictionValidator{client: c, decoder: decoder(t)}
	resp := v.Handle(context.Background(), evictionReq(t, "app", "web-a"))
	if resp.Allowed {
		t.Fatal("non-NotFound Get must not fail-open")
	}
	if resp.Result == nil || resp.Result.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 errored, got %+v", resp)
	}
}

type errGetClient struct {
	client.Client
	err error
}

func (c *errGetClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if c.err != nil {
		return c.err
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func evictionReq(t *testing.T, ns, name string) admission.Request {
	t.Helper()
	ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Name:      name,
		Namespace: ns,
		Kind:      metav1.GroupVersionKind{Group: "policy", Version: "v1", Kind: "Eviction"},
		Object:    runtime.RawExtension{Raw: raw},
	}}
}

func mustReq(t *testing.T, obj runtime.Object) admission.Request {
	t.Helper()
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Object: runtime.RawExtension{Raw: raw},
	}}
}
