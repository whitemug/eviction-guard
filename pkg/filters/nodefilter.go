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
// Label selectors and namePattern are validated once here; Matches reuses the compiled form.
func FromSpec(spec egv1a1.NodeFilter) (Filter, error) {
	var include, exclude labels.Selector
	if spec.LabelSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(spec.LabelSelector)
		if err != nil {
			return nil, fmt.Errorf("labelSelector: %w", err)
		}
		include = sel
	}
	if spec.ExcludeLabelSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(spec.ExcludeLabelSelector)
		if err != nil {
			return nil, fmt.Errorf("excludeLabelSelector: %w", err)
		}
		exclude = sel
	}
	if spec.NamePattern != "" {
		if _, err := filepath.Match(spec.NamePattern, ""); err != nil {
			return nil, fmt.Errorf("namePattern: %w", err)
		}
	}
	return &specFilter{
		spec:    spec,
		include: include,
		exclude: exclude,
	}, nil
}

type specFilter struct {
	spec    egv1a1.NodeFilter
	include labels.Selector
	exclude labels.Selector
}

func (s *specFilter) Name() string { return "nodeFilter" }

func (s *specFilter) Matches(node *corev1.Node) (bool, error) {
	if node == nil {
		return false, fmt.Errorf("node is nil")
	}

	if s.include != nil && !s.include.Matches(labels.Set(node.Labels)) {
		return false, nil
	}

	if s.exclude != nil && s.exclude.Matches(labels.Set(node.Labels)) {
		return false, nil
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
			if !want.MatchesAny(node.Spec.Taints) {
				return false, nil
			}
		}
	}

	return true, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
