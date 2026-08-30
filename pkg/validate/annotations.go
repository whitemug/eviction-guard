/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package validate checks Eviction Guard workload annotations and policy fields
// so they can be rejected at admission time (webhook) as well as in tests.
package validate

import (
	"fmt"
	"path/filepath"
	"time"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/backends"
)

// WorkloadAnnotations checks eviction-guard.io/* annotations on a Deployment.
// Empty annotations are valid (the policy supplies defaults).
func WorkloadAnnotations(namespace string, ann map[string]string) error {
	if ann == nil {
		return nil
	}

	var list []egv1a1.ScaleBackendType
	if raw := ann[egv1a1.ScaleBackendAnnotation]; raw != "" {
		parsed, err := backends.ParseList(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", egv1a1.ScaleBackendAnnotation, err)
		}
		list = parsed
	}

	if raw := ann[egv1a1.ScaleBackAfterAnnotation]; raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("%s: %w (want a Go duration such as 15m or 1h)", egv1a1.ScaleBackAfterAnnotation, err)
		}
		if d <= 0 {
			return fmt.Errorf("%s must be greater than 0", egv1a1.ScaleBackAfterAnnotation)
		}
	}

	if raw := ann[egv1a1.HPATargetAnnotation]; raw != "" {
		if _, err := backends.ParseScaleTarget(raw, namespace); err != nil {
			return fmt.Errorf("%s: %w", egv1a1.HPATargetAnnotation, err)
		}
	}

	if raw := ann[egv1a1.ScaleTargetAnnotation]; raw != "" {
		t, err := backends.ParseScaleTarget(raw, namespace)
		if err != nil {
			return fmt.Errorf("%s: %w", egv1a1.ScaleTargetAnnotation, err)
		}
		if hasBackend(list, egv1a1.ScaleBackendCRD) && (t.APIVersion == "" || t.Kind == "") {
			return fmt.Errorf("crd backend requires %s as group/version/namespaces/ns/kind/name", egv1a1.ScaleTargetAnnotation)
		}
	} else if hasBackend(list, egv1a1.ScaleBackendCRD) {
		return fmt.Errorf("crd backend requires annotation %s", egv1a1.ScaleTargetAnnotation)
	}

	if raw := ann[egv1a1.StampAnnotation]; raw != "" && raw != "true" && raw != "false" {
		return fmt.Errorf("%s must be \"true\" or \"false\"", egv1a1.StampAnnotation)
	}
	return nil
}

// Policy checks EvictionGuardPolicy fields the CRD schema cannot express
// (for example filepath.Match syntax on namePattern).
func Policy(p *egv1a1.EvictionGuardPolicy) error {
	if p == nil {
		return nil
	}
	if pat := p.Spec.NodeFilter.NamePattern; pat != "" {
		if _, err := filepath.Match(pat, ""); err != nil {
			return fmt.Errorf("spec.nodeFilter.namePattern: %w", err)
		}
	}
	return nil
}

func hasBackend(list []egv1a1.ScaleBackendType, want egv1a1.ScaleBackendType) bool {
	for _, b := range list {
		if b == want {
			return true
		}
	}
	return false
}
