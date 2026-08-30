/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package webhook

import (
	"context"
	"net/http"

	appsv1 "k8s.io/api/apps/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/validate"
)

const (
	// DeploymentPath is the HTTP path registered on the webhook server.
	DeploymentPath = "/validate-apps-v1-deployment"
	// PolicyPath is the HTTP path for EvictionGuardPolicy.
	PolicyPath = "/validate-eviction-guard-io-v1alpha1-evictionguardpolicy"
)

// SetupWithManager registers validating admission handlers.
func SetupWithManager(mgr ctrl.Manager) error {
	dec := admission.NewDecoder(mgr.GetScheme())
	server := mgr.GetWebhookServer()
	server.Register(DeploymentPath, &webhook.Admission{Handler: &deploymentValidator{decoder: dec}})
	server.Register(PolicyPath, &webhook.Admission{Handler: &policyValidator{decoder: dec}})
	return nil
}

type deploymentValidator struct {
	decoder admission.Decoder
}

func (v *deploymentValidator) Handle(_ context.Context, req admission.Request) admission.Response {
	dep := &appsv1.Deployment{}
	if err := v.decoder.Decode(req, dep); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if err := validate.WorkloadAnnotations(dep.Namespace, dep.Annotations); err != nil {
		return admission.Denied(err.Error())
	}
	return admission.Allowed("")
}

type policyValidator struct {
	decoder admission.Decoder
}

func (v *policyValidator) Handle(_ context.Context, req admission.Request) admission.Response {
	p := &egv1a1.EvictionGuardPolicy{}
	if err := v.decoder.Decode(req, p); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	if err := validate.Policy(p); err != nil {
		return admission.Denied(err.Error())
	}
	return admission.Allowed("")
}
