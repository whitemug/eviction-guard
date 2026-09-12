/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package backends

import (
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

// Binding is one scale-backend token: catalog key plus object name.
type Binding struct {
	Key       string
	Namespace string
	Name      string
}

// ResolvedStep is a catalog binding ready for planning (one integer path).
type ResolvedStep struct {
	Key             string
	Target          Target
	Annotations     []string
	SkipDownscaling bool
}

// ParseBindings decodes eviction-guard.io/scale-backend as key[=name] tokens.
// Bare key or key= defaults Name to defaultName and Namespace to defaultNS.
// key=ns/name sets both. key=name uses defaultNS.
func ParseBindings(raw, defaultNS, defaultName string) ([]Binding, error) {
	var out []Binding
	seen := map[string]bool{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		key, ns, name, err := splitBinding(p, defaultNS, defaultName)
		if err != nil {
			return nil, err
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Binding{Key: key, Namespace: ns, Name: name})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty scale-backend list")
	}
	return out, nil
}

func splitBinding(tok, defaultNS, defaultName string) (key, ns, name string, err error) {
	key, rest, hasEq := strings.Cut(tok, "=")
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", "", fmt.Errorf("empty scale-backend key in %q", tok)
	}
	if !hasEq || strings.TrimSpace(rest) == "" {
		return key, defaultNS, defaultName, nil
	}
	rest = strings.TrimSpace(rest)
	if strings.Contains(rest, "/") {
		parts := strings.Split(rest, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", "", fmt.Errorf("invalid scale-backend target %q (want name or ns/name)", tok)
		}
		return key, parts[0], parts[1], nil
	}
	return key, defaultNS, rest, nil
}

// BindingsForWorkload returns bindings from eviction-guard.io/scale-backend.
// The annotation is required. Token order is patch order for entries that have
// integer paths; the first *patched* path is the SpareReady primary. Catalog
// entries with no paths are external (declared only).
func BindingsForWorkload(dep *appsv1.Deployment, policy *egv1a1.EvictionGuardPolicy) ([]Binding, error) {
	if dep == nil || policy == nil {
		return nil, fmt.Errorf("deployment and policy required")
	}
	raw := ""
	if dep.Annotations != nil {
		raw = dep.Annotations[egv1a1.ScaleBackendAnnotation]
	}
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%s is required (list policy.spec.backends keys; first patched key is SpareReady primary)",
			egv1a1.ScaleBackendAnnotation)
	}
	return ParseBindings(raw, dep.Namespace, dep.Name)
}

// ResolveCatalog maps bindings through policy.Spec.Backends.
// Each integer path becomes a step. Entries with no paths are skipped (external).
// Empty result is valid when every bound entry is external.
func ResolveCatalog(dep *appsv1.Deployment, policy *egv1a1.EvictionGuardPolicy, bindings []Binding) ([]ResolvedStep, error) {
	if policy == nil || len(policy.Spec.Backends) == 0 {
		return nil, fmt.Errorf("policy.spec.backends is required")
	}
	var out []ResolvedStep
	for _, b := range bindings {
		entry, ok := policy.Spec.Backends[b.Key]
		if !ok {
			return nil, fmt.Errorf("unknown scale-backend key %q (not in policy.spec.backends)", b.Key)
		}
		paths, anns, err := splitPatches(entry.Patches)
		if err != nil {
			return nil, fmt.Errorf("backends[%s]: %w", b.Key, err)
		}
		if len(paths) == 0 {
			continue
		}
		for i, path := range paths {
			stepAnns := []string(nil)
			if i == 0 {
				stepAnns = anns
			}
			out = append(out, ResolvedStep{
				Key: b.Key,
				Target: Target{
					ObjectKey:  client.ObjectKey{Namespace: b.Namespace, Name: b.Name},
					APIVersion: entry.APIVersion,
					Kind:       entry.Kind,
					FieldPath:  path,
				},
				Annotations:     stepAnns,
				SkipDownscaling: entry.SkipDownscaling,
			})
		}
	}
	return out, nil
}

func splitPatches(patches []egv1a1.BackendPatch) (paths []string, anns []string, err error) {
	seenPath := map[string]bool{}
	for _, p := range patches {
		if p.Path == "" && p.Annotation == "" {
			return nil, nil, fmt.Errorf("patch must set path or annotation")
		}
		if p.Path != "" {
			if seenPath[p.Path] {
				return nil, nil, fmt.Errorf("duplicate path %q", p.Path)
			}
			seenPath[p.Path] = true
			paths = append(paths, p.Path)
		}
		if p.Annotation != "" {
			anns = append(anns, p.Annotation)
		}
	}
	return paths, anns, nil
}
