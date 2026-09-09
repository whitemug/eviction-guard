/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/filters"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

var _ handler.EventHandler = nodePolicyHandler{}

// nodePolicyHandler enqueues policies whose nodeFilter matches the node.
// Updates union old and new so a node leaving a filter still wakes that policy.
type nodePolicyHandler struct {
	client.Client
}

func (h nodePolicyHandler) Create(ctx context.Context, e event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	enqueuePolicies(q, policiesForObject(ctx, h.Client, e.Object))
}

func (h nodePolicyHandler) Delete(ctx context.Context, e event.TypedDeleteEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	enqueuePolicies(q, policiesForObject(ctx, h.Client, e.Object))
}

func (h nodePolicyHandler) Generic(ctx context.Context, e event.TypedGenericEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	enqueuePolicies(q, policiesForObject(ctx, h.Client, e.Object))
}

func (h nodePolicyHandler) Update(ctx context.Context, e event.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	oldN, _ := e.ObjectOld.(*corev1.Node)
	newN, _ := e.ObjectNew.(*corev1.Node)
	enqueuePolicies(q, policiesForNodeUpdate(ctx, h.Client, oldN, newN))
}

func enqueuePolicies(q workqueue.TypedRateLimitingInterface[reconcile.Request], reqs []reconcile.Request) {
	for _, req := range reqs {
		q.Add(req)
	}
}

func policiesForObject(ctx context.Context, c client.Client, obj client.Object) []reconcile.Request {
	n, ok := obj.(*corev1.Node)
	if !ok {
		return nil
	}
	return policiesForNode(ctx, c, n, false)
}

// policiesForNodeUpdate unions policies matching the old or new node (filter exit).
func policiesForNodeUpdate(ctx context.Context, c client.Client, oldN, newN *corev1.Node) []reconcile.Request {
	seen := map[string]struct{}{}
	var out []reconcile.Request
	for _, n := range []*corev1.Node{oldN, newN} {
		for _, req := range policiesForNode(ctx, c, n, false) {
			if _, ok := seen[req.Name]; ok {
				continue
			}
			seen[req.Name] = struct{}{}
			out = append(out, req)
		}
	}
	return out
}

// mapPodToPolicies enqueues only policies whose nodeFilter matches the pod's
// node and that node is currently vulnerable. Ready/status noise on healthy
// nodes stays on the window controller (SpareReady).
func (r *PolicyReconciler) mapPodToPolicies(ctx context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok || pod.Spec.NodeName == "" {
		return nil
	}
	node := &corev1.Node{}
	if err := r.Get(ctx, types.NamespacedName{Name: pod.Spec.NodeName}, node); err != nil {
		return nil
	}
	return policiesForNode(ctx, r.Client, node, true)
}

// policiesForNode returns policies whose NodeFilter matches node.
// If onlyVulnerable is set, the node must also carry a disruption signal for that policy.
func policiesForNode(ctx context.Context, c client.Client, node *corev1.Node, onlyVulnerable bool) []reconcile.Request {
	if node == nil {
		return nil
	}
	list := &egv1a1.EvictionGuardPolicyList{}
	if err := c.List(ctx, list); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		p := &list.Items[i]
		f, err := filters.FromSpec(p.Spec.NodeFilter)
		if err != nil {
			continue
		}
		ok, err := f.Matches(node)
		if err != nil || !ok {
			continue
		}
		if onlyVulnerable && !signals.Vulnerable(node, p) {
			continue
		}
		out = append(out, reconcile.Request{NamespacedName: types.NamespacedName{Name: p.Name}})
	}
	return out
}
