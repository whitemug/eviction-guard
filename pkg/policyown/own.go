/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package policyown resolves which EvictionGuardPolicy owns a Deployment when
// more than one policy could apply.
package policyown

import (
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

// Pin returns the Deployment's eviction-guard.io/policy-pin annotation, or "".
func Pin(dep *appsv1.Deployment) string {
	if dep == nil || dep.Annotations == nil {
		return ""
	}
	return strings.TrimSpace(dep.Annotations[egv1a1.PolicyPinAnnotation])
}

// WorkloadSelectorMatch reports whether policy.Spec.WorkloadSelector allows dep.
// Empty selector matches all Deployments.
func WorkloadSelectorMatch(policy *egv1a1.EvictionGuardPolicy, dep *appsv1.Deployment) bool {
	if policy == nil || dep == nil {
		return false
	}
	if policy.Spec.WorkloadSelector == nil {
		return true
	}
	sel, err := metav1.LabelSelectorAsSelector(policy.Spec.WorkloadSelector)
	if err != nil {
		return false
	}
	return sel.Matches(labels.Set(dep.Labels))
}

// NamespaceSelectorMatch reports whether policy.Spec.NamespaceSelector allows
// the namespace labeled by nsLabels. Empty selector matches all namespaces.
func NamespaceSelectorMatch(policy *egv1a1.EvictionGuardPolicy, nsLabels labels.Set) bool {
	if policy == nil {
		return false
	}
	if policy.Spec.NamespaceSelector == nil {
		return true
	}
	sel, err := metav1.LabelSelectorAsSelector(policy.Spec.NamespaceSelector)
	if err != nil {
		return false
	}
	if nsLabels == nil {
		nsLabels = labels.Set{}
	}
	return sel.Matches(nsLabels)
}

// SelectorMatch is namespaceSelector AND workloadSelector for dep.
func SelectorMatch(policy *egv1a1.EvictionGuardPolicy, dep *appsv1.Deployment, nsLabels labels.Set) bool {
	return NamespaceSelectorMatch(policy, nsLabels) && WorkloadSelectorMatch(policy, dep)
}

// Owns reports whether policy should manage dep.
//
// Rules:
//   - If dep pins a policy name, only that policy owns (selectors ignored).
//   - Otherwise among non-deleting policies that pass namespace+workload selectors
//     for dep, the lexicographically first policy name wins.
//
// Node filters are not considered here; callers still apply nodeFilter/signals.
func Owns(policy *egv1a1.EvictionGuardPolicy, dep *appsv1.Deployment, nsLabels labels.Set, all []*egv1a1.EvictionGuardPolicy) bool {
	if policy == nil || dep == nil || !policy.DeletionTimestamp.IsZero() {
		return false
	}
	if pin := Pin(dep); pin != "" {
		return pin == policy.Name
	}
	if !SelectorMatch(policy, dep, nsLabels) {
		return false
	}
	winner := FirstByName(dep, nsLabels, all)
	return winner != nil && winner.Name == policy.Name
}

// FirstByName returns the lexicographically first non-deleting policy whose
// namespace+workload selectors match dep. Ignores pins (callers should check Pin first).
func FirstByName(dep *appsv1.Deployment, nsLabels labels.Set, all []*egv1a1.EvictionGuardPolicy) *egv1a1.EvictionGuardPolicy {
	var names []string
	byName := map[string]*egv1a1.EvictionGuardPolicy{}
	for _, p := range all {
		if p == nil || !p.DeletionTimestamp.IsZero() {
			continue
		}
		if !SelectorMatch(p, dep, nsLabels) {
			continue
		}
		if _, ok := byName[p.Name]; ok {
			continue
		}
		byName[p.Name] = p
		names = append(names, p.Name)
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)
	return byName[names[0]]
}

// ResolveOwner picks the owning policy for dep.
// Pinned name wins if that policy exists and is not deleting; otherwise first-by-name
// among selector matches. Returns nil when nothing owns the workload.
func ResolveOwner(dep *appsv1.Deployment, nsLabels labels.Set, all []*egv1a1.EvictionGuardPolicy) *egv1a1.EvictionGuardPolicy {
	if dep == nil {
		return nil
	}
	if pin := Pin(dep); pin != "" {
		for _, p := range all {
			if p == nil || !p.DeletionTimestamp.IsZero() {
				continue
			}
			if p.Name == pin {
				return p
			}
		}
		return nil
	}
	return FirstByName(dep, nsLabels, all)
}
