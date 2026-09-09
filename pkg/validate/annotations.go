/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package validate checks Eviction Guard workload annotations and policy fields
// so they can be rejected at admission time (webhook) as well as in tests.
package validate

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/backends"
)

var (
	dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// ScaleBackendRequired reports an error when eviction-guard.io/scale-backend is
// missing or blank. Protected workloads must set it (no default catalog bind).
func ScaleBackendRequired(ann map[string]string) error {
	raw := ""
	if ann != nil {
		raw = ann[egv1a1.ScaleBackendAnnotation]
	}
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("%s is required (list policy.spec.backends keys; first key is SpareReady primary)",
			egv1a1.ScaleBackendAnnotation)
	}
	return nil
}

// WorkloadAnnotations checks eviction-guard.io/* annotations on a Deployment.
// When scale-backend is set, its token syntax is validated. Call
// ScaleBackendRequired separately when the pod template is protected.
func WorkloadAnnotations(namespace string, ann map[string]string) error {
	if ann == nil {
		return nil
	}

	if raw := ann[egv1a1.ScaleBackendAnnotation]; raw != "" {
		if _, err := backends.ParseBindings(raw, namespace, "workload"); err != nil {
			return fmt.Errorf("%s: %w", egv1a1.ScaleBackendAnnotation, err)
		}
	}

	if raw := ann[egv1a1.ScaleBackAfterAnnotation]; raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("%s: %w (want a Go duration such as 1m or 1h)", egv1a1.ScaleBackAfterAnnotation, err)
		}
		if d <= 0 {
			return fmt.Errorf("%s must be greater than 0", egv1a1.ScaleBackAfterAnnotation)
		}
	}

	if raw := ann[egv1a1.StampAnnotation]; raw != "" && raw != "true" && raw != "false" {
		return fmt.Errorf("%s must be \"true\" or \"false\"", egv1a1.StampAnnotation)
	}

	if raw := strings.TrimSpace(ann[egv1a1.PolicyPinAnnotation]); raw != "" {
		if len(raw) > 63 || !dns1123Label.MatchString(raw) {
			return fmt.Errorf("%s must be a DNS-1123 label (policy name)", egv1a1.PolicyPinAnnotation)
		}
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
	if len(p.Spec.Backends) == 0 {
		return fmt.Errorf("spec.backends is required")
	}
	for key, entry := range p.Spec.Backends {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("spec.backends: empty key")
		}
		if len(key) > 63 || !dns1123Label.MatchString(key) {
			return fmt.Errorf("spec.backends[%s]: key must be a DNS-1123 label", key)
		}
		if entry.APIVersion == "" || entry.Kind == "" {
			return fmt.Errorf("spec.backends[%s]: apiVersion and kind are required", key)
		}
		if len(entry.Patches) == 0 {
			continue
		}
		paths := 0
		seenPath := map[string]bool{}
		for i, patch := range entry.Patches {
			if patch.Path == "" && patch.Annotation == "" {
				return fmt.Errorf("spec.backends[%s].patches[%d]: set path or annotation", key, i)
			}
			if patch.Path != "" {
				if seenPath[patch.Path] {
					return fmt.Errorf("spec.backends[%s]: duplicate path %q", key, patch.Path)
				}
				seenPath[patch.Path] = true
				paths++
			}
		}
		// Zero paths (annotation-only or empty) is allowed: external scaler entry.
	}
	return nil
}
