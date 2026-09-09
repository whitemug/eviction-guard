/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

// Package filters evaluates EvictionGuardPolicy.spec.nodeFilter against Node objects.
package filters

import (
	"fmt"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

const (
	zoneLabel         = "topology.kubernetes.io/zone"
	instanceTypeLabel = "node.kubernetes.io/instance-type"
	capacityTypeLabel = "karpenter.sh/capacity-type"
)

// Filter decides whether a node is in a policy's watch set.
type Filter interface {
	Name() string
	Matches(node *corev1.Node) (bool, error)
}

// FromSpec compiles the CRD NodeFilter into a Filter. Empty spec matches all nodes.
func FromSpec(spec egv1a1.NodeFilter) (Filter, error) {
	return &specFilter{spec: spec}, nil
}

type specFilter struct {
	spec egv1a1.NodeFilter
}

func (s *specFilter) Name() string { return "nodeFilter" }

func (s *specFilter) Matches(node *corev1.Node) (bool, error) {
	if node == nil {
		return false, fmt.Errorf("node is nil")
	}

	if s.spec.LabelSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(s.spec.LabelSelector)
		if err != nil {
			return false, fmt.Errorf("labelSelector: %w", err)
		}
		if !sel.Matches(labels.Set(node.Labels)) {
			return false, nil
		}
	}

	if s.spec.ExcludeLabelSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(s.spec.ExcludeLabelSelector)
		if err != nil {
			return false, fmt.Errorf("excludeLabelSelector: %w", err)
		}
		if sel.Matches(labels.Set(node.Labels)) {
			return false, nil
		}
	}

	if len(s.spec.Names) > 0 && !contains(s.spec.Names, node.Name) {
		return false, nil
	}

	if s.spec.NamePattern != "" {
		ok, err := filepath.Match(s.spec.NamePattern, node.Name)
		if err != nil {
			return false, fmt.Errorf("namePattern: %w", err)
		}
		if !ok {
			return false, nil
		}
	}

	if len(s.spec.Zones) > 0 && !contains(s.spec.Zones, node.Labels[zoneLabel]) {
		return false, nil
	}

	if len(s.spec.InstanceTypes) > 0 && !contains(s.spec.InstanceTypes, node.Labels[instanceTypeLabel]) {
		return false, nil
	}

	if len(s.spec.CapacityTypes) > 0 && !contains(s.spec.CapacityTypes, node.Labels[capacityTypeLabel]) {
		return false, nil
	}

	if len(s.spec.TaintSelector) > 0 {
		for _, want := range s.spec.TaintSelector {
			if !taintMatches(node.Spec.Taints, want) {
				return false, nil
			}
		}
	}

	return true, nil
}

func taintMatches(taints []corev1.Taint, want egv1a1.TaintMatch) bool {
	for _, t := range taints {
		if t.Key != want.Key {
			continue
		}
		if want.Value != "" && t.Value != want.Value {
			continue
		}
		if want.Effect != "" && t.Effect != want.Effect {
			continue
		}
		return true
	}
	return false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
