/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
)

// Field index keys for the shared manager cache (controller-runtime client).
const (
	IndexPodNodeName = "spec.nodeName"

	// IndexWindowPolicyName indexes EvictionGuardWindow by Spec.PolicyName.
	IndexWindowPolicyName = "spec.policyName"
	// IndexWindowWorkload indexes EvictionGuardWindow by "namespace/name" of Spec.Target.
	IndexWindowWorkload = "spec.target"
	// IndexWindowVulnerableNode indexes EvictionGuardWindow by each Spec.VulnerableNodes entry.
	IndexWindowVulnerableNode = "spec.vulnerableNodes"
)

var (
	podIndexesOnce    sync.Once
	podIndexesErr     error
	windowIndexesOnce sync.Once
	windowIndexesErr  error
)

func registerPodIndexes(mgr manager.Manager) error {
	podIndexesOnce.Do(func() {
		podIndexesErr = mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, IndexPodNodeName, func(o client.Object) []string {
			name := o.(*corev1.Pod).Spec.NodeName
			if name == "" {
				return nil
			}
			return []string{name}
		})
	})
	return podIndexesErr
}

func registerWindowIndexes(mgr manager.Manager) error {
	windowIndexesOnce.Do(func() {
		idx := mgr.GetFieldIndexer()
		if err := idx.IndexField(context.Background(), &egv1a1.EvictionGuardWindow{}, IndexWindowPolicyName, func(o client.Object) []string {
			name := o.(*egv1a1.EvictionGuardWindow).Spec.PolicyName
			if name == "" {
				return nil
			}
			return []string{name}
		}); err != nil {
			windowIndexesErr = err
			return
		}
		if err := idx.IndexField(context.Background(), &egv1a1.EvictionGuardWindow{}, IndexWindowWorkload, func(o client.Object) []string {
			w := o.(*egv1a1.EvictionGuardWindow)
			ns, name := w.Spec.Target.Namespace, w.Spec.Target.Name
			if ns == "" {
				ns = w.Namespace
			}
			if name == "" {
				return nil
			}
			return []string{workloadIndexKey(ns, name)}
		}); err != nil {
			windowIndexesErr = err
			return
		}
		windowIndexesErr = idx.IndexField(context.Background(), &egv1a1.EvictionGuardWindow{}, IndexWindowVulnerableNode, func(o client.Object) []string {
			nodes := o.(*egv1a1.EvictionGuardWindow).Spec.VulnerableNodes
			if len(nodes) == 0 {
				return nil
			}
			out := make([]string, 0, len(nodes))
			seen := map[string]struct{}{}
			for _, n := range nodes {
				if n == "" {
					continue
				}
				if _, ok := seen[n]; ok {
					continue
				}
				seen[n] = struct{}{}
				out = append(out, n)
			}
			return out
		})
	})
	return windowIndexesErr
}

func workloadIndexKey(ns, name string) string {
	return fmt.Sprintf("%s/%s", ns, name)
}
