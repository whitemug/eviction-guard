/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package webhook

import (
	"context"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/whitemug/eviction-guard/pkg/evictgate"
	"github.com/whitemug/eviction-guard/pkg/metrics"
)

const (
	// EvictionPath is the HTTP path for pods/eviction admission.
	EvictionPath = "/validate-core-v1-eviction"
)

type evictionValidator struct {
	client  client.Client
	decoder admission.Decoder
}

func (v *evictionValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	ev := &policyv1.Eviction{}
	if err := v.decoder.Decode(req, ev); err != nil {
		// Some clients send only Request kind/name; fall back to request metadata.
		if req.Name == "" || req.Namespace == "" {
			return admission.Errored(http.StatusBadRequest, err)
		}
		ev.Name = req.Name
		ev.Namespace = req.Namespace
	}
	ns := ev.Namespace
	if ns == "" {
		ns = req.Namespace
	}
	name := ev.Name
	if name == "" {
		name = req.Name
	}
	pod := &corev1.Pod{}
	if err := v.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, pod); err != nil {
		if apierrors.IsNotFound(err) {
			// Pod already gone — allow.
			return admission.Allowed("pod not found")
		}
		// Transient / auth failures must not fail-open while the webhook is up
		// (matches failurePolicy: Fail — deny under uncertainty).
		return admission.Errored(http.StatusInternalServerError, err)
	}

	res, err := evictgate.Evaluate(ctx, v.client, pod)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	if res.Decision == evictgate.Deny {
		metrics.EvictionDecisions.WithLabelValues("deny").Inc()
		return admission.Denied(res.Reason)
	}
	metrics.EvictionDecisions.WithLabelValues("allow").Inc()
	return admission.Allowed(res.Reason)
}

// registerEviction adds the pods/eviction validating handler.
func registerEviction(mgr ctrl.Manager, dec admission.Decoder) {
	mgr.GetWebhookServer().Register(EvictionPath, &webhook.Admission{
		Handler: &evictionValidator{client: mgr.GetClient(), decoder: dec},
	})
}
