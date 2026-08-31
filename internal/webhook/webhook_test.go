/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package webhook

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
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
	}
	resp := v.Handle(context.Background(), mustReq(t, ok))
	if !resp.Allowed {
		t.Fatalf("allowed=false: %s", resp.Result.Message)
	}

	bad := ok.DeepCopy()
	bad.Annotations[egv1a1.ScaleBackendAnnotation] = "nope"
	resp = v.Handle(context.Background(), mustReq(t, bad))
	if resp.Allowed {
		t.Fatal("expected deny")
	}
}

func TestPolicyWebhook(t *testing.T) {
	v := &policyValidator{decoder: decoder(t)}
	ok := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot"},
		Spec:       egv1a1.EvictionGuardPolicySpec{NodeFilter: egv1a1.NodeFilter{NamePattern: "spot-*"}},
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
