/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/backends"
)

func resolveActionTarget(dep *appsv1.Deployment, backend egv1a1.ScaleBackendType) (backends.Target, error) {
	ann := dep.Annotations
	if ann == nil {
		ann = map[string]string{}
	}
	switch backend {
	case egv1a1.ScaleBackendDeployment:
		return backends.Target{
			ObjectKey:  client.ObjectKey{Namespace: dep.Namespace, Name: dep.Name},
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		}, nil
	case egv1a1.ScaleBackendHPAMin:
		t := backends.Target{
			ObjectKey:  client.ObjectKey{Namespace: dep.Namespace, Name: dep.Name},
			APIVersion: "autoscaling/v1",
			Kind:       "HorizontalPodAutoscaler",
		}
		if raw := ann[egv1a1.HPATargetAnnotation]; raw != "" {
			parsed, err := backends.ParseScaleTarget(raw, dep.Namespace)
			if err != nil {
				return backends.Target{}, err
			}
			t.Namespace = parsed.Namespace
			t.Name = parsed.Name
			return t, nil
		}
		if raw := ann[egv1a1.ScaleTargetAnnotation]; raw != "" {
			parsed, err := backends.ParseScaleTarget(raw, dep.Namespace)
			if err != nil {
				return backends.Target{}, err
			}
			if parsed.Kind == "" {
				t.Namespace = parsed.Namespace
				t.Name = parsed.Name
			}
		}
		return t, nil
	case egv1a1.ScaleBackendCRD:
		raw := ann[egv1a1.ScaleTargetAnnotation]
		if raw == "" {
			return backends.Target{}, fmt.Errorf("crd backend requires annotation %s", egv1a1.ScaleTargetAnnotation)
		}
		parsed, err := backends.ParseScaleTarget(raw, dep.Namespace)
		if err != nil {
			return backends.Target{}, err
		}
		if parsed.APIVersion == "" || parsed.Kind == "" {
			return backends.Target{}, fmt.Errorf("crd backend %s must be group/version/namespaces/ns/kind/name", egv1a1.ScaleTargetAnnotation)
		}
		parsed.FieldPath = ann[egv1a1.CRDReplicasPathAnnotation]
		return parsed, nil
	default:
		raw := ann[egv1a1.ScaleTargetAnnotation]
		if raw == "" {
			return backends.Target{}, fmt.Errorf("backend %q requires annotation %s", backend, egv1a1.ScaleTargetAnnotation)
		}
		parsed, err := backends.ParseScaleTarget(raw, dep.Namespace)
		if err != nil {
			return backends.Target{}, err
		}
		if parsed.APIVersion == "" || parsed.Kind == "" {
			return backends.Target{}, fmt.Errorf("backend %q %s must be group/version/namespaces/ns/kind/name", backend, egv1a1.ScaleTargetAnnotation)
		}
		parsed.FieldPath = ann[egv1a1.CRDReplicasPathAnnotation]
		return parsed, nil
	}
}

func scaleAction(backend egv1a1.ScaleBackendType, t backends.Target, baseline, scaledTo int32, stampKeys []string) egv1a1.ScaleAction {
	return egv1a1.ScaleAction{
		Backend:    backend,
		APIVersion: t.APIVersion,
		Kind:       t.Kind,
		Namespace:  t.Namespace,
		Name:       t.Name,
		FieldPath:  t.FieldPath,
		Baseline:   baseline,
		ScaledTo:   scaledTo,
		StampKeys:  stampKeys,
	}
}

func actionTarget(a egv1a1.ScaleAction) backends.Target {
	return backends.Target{
		ObjectKey:  client.ObjectKey{Namespace: a.Namespace, Name: a.Name},
		APIVersion: a.APIVersion,
		Kind:       a.Kind,
		FieldPath:  a.FieldPath,
	}
}

func actionKey(a egv1a1.ScaleAction) string {
	return string(a.Backend) + "/" + a.APIVersion + "/" + a.Kind + "/" + a.Namespace + "/" + a.Name
}

func findAction(actions []egv1a1.ScaleAction, backend egv1a1.ScaleBackendType, t backends.Target) *egv1a1.ScaleAction {
	want := actionKey(scaleAction(backend, t, 0, 0, nil))
	for i := range actions {
		if actionKey(actions[i]) == want {
			return &actions[i]
		}
	}
	return nil
}

func mergeActions(existing, next []egv1a1.ScaleAction) []egv1a1.ScaleAction {
	seen := map[string]bool{}
	out := make([]egv1a1.ScaleAction, 0, len(existing)+len(next))
	for _, a := range next {
		out = append(out, a)
		seen[actionKey(a)] = true
	}
	for _, a := range existing {
		if !seen[actionKey(a)] {
			out = append(out, a)
		}
	}
	return out
}

func objectRef(t backends.Target) *corev1.ObjectReference {
	return &corev1.ObjectReference{
		APIVersion: t.APIVersion,
		Kind:       t.Kind,
		Namespace:  t.Namespace,
		Name:       t.Name,
	}
}

func primaryBackendRef(dep *appsv1.Deployment, a egv1a1.ScaleAction) *corev1.ObjectReference {
	if a.Backend == egv1a1.ScaleBackendDeployment && a.Name == dep.Name && a.Namespace == dep.Namespace {
		return nil
	}
	return objectRef(actionTarget(a))
}

func hasBackend(actions []egv1a1.ScaleAction, t egv1a1.ScaleBackendType) bool {
	for _, a := range actions {
		if a.Backend == t {
			return true
		}
	}
	return false
}

func scaleBackAfter(policy *egv1a1.EvictionGuardPolicy, dep *appsv1.Deployment) time.Duration {
	if dep != nil && dep.Annotations != nil {
		if raw := dep.Annotations[egv1a1.ScaleBackAfterAnnotation]; raw != "" {
			d, err := time.ParseDuration(raw)
			if err == nil && d > 0 {
				return d
			}
		}
	}
	return policy.ScaleBackAfterOrDefault().Duration
}

type resourceStamp struct {
	enabled                    bool
	scaledTo, baseline, active string
	windowUntil                string
}

func resolveStamp(policy *egv1a1.EvictionGuardPolicy, dep *appsv1.Deployment) resourceStamp {
	s := policy.Spec.Stamp
	out := resourceStamp{
		enabled:     s.Enabled,
		scaledTo:    pickKey(s.ScaledToKey, egv1a1.StampScaledToKey),
		baseline:    pickKey(s.BaselineKey, egv1a1.StampBaselineKey),
		active:      pickKey(s.ActiveKey, egv1a1.StampActiveKey),
		windowUntil: pickKey(s.WindowUntilKey, egv1a1.StampWindowUntilKey),
	}
	if dep == nil || dep.Annotations == nil {
		return out
	}
	ann := dep.Annotations
	switch ann[egv1a1.StampAnnotation] {
	case "true":
		out.enabled = true
	case "false":
		out.enabled = false
	}
	if v := ann[egv1a1.StampScaledToKeyAnnotation]; v != "" {
		out.scaledTo = v
	}
	if v := ann[egv1a1.StampBaselineKeyAnnotation]; v != "" {
		out.baseline = v
	}
	if v := ann[egv1a1.StampActiveKeyAnnotation]; v != "" {
		out.active = v
	}
	if v := ann[egv1a1.StampWindowUntilKeyAnnotation]; v != "" {
		out.windowUntil = v
	}
	return out
}

func pickKey(configured, fallback string) string {
	if configured != "" {
		return configured
	}
	return fallback
}

func (s resourceStamp) keys() []string {
	if !s.enabled {
		return nil
	}
	keys := []string{s.scaledTo, s.baseline, s.active, s.windowUntil}
	sort.Strings(keys)
	return keys
}

func (s resourceStamp) values(baseline, desired int32) map[string]string {
	if !s.enabled {
		return nil
	}
	return map[string]string{
		s.scaledTo:    strconv.FormatInt(int64(desired), 10),
		s.baseline:    strconv.FormatInt(int64(baseline), 10),
		s.active:      "true",
		s.windowUntil: "",
	}
}
