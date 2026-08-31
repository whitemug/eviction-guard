/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/filters"
	"github.com/whitemug/eviction-guard/pkg/metrics"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

const (
	IndexPodNodeName = "spec.nodeName"

	reasonScaledUp      = "ScaledUp"
	reasonPolicyReady   = "Ready"
	reasonPolicyError   = "ReconcileError"
	reasonWindowOpened  = "WindowOpened"
	reasonWindowsCapped = "WindowsCapped"
	requeueDeferred     = 15 * time.Second
)

// PolicyReconciler watches Nodes (and related objects) and opens disruption windows
// for opted-in workloads that sit on filtered, vulnerable nodes.
type PolicyReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Now      func() time.Time
}

func (r *PolicyReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// +kubebuilder:rbac:groups=eviction-guard.io,resources=evictionguardpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=eviction-guard.io,resources=evictionguardpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=eviction-guard.io,resources=evictionguardpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups=eviction-guard.io,resources=evictionguardwindows,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=eviction-guard.io,resources=evictionguardwindows/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch

func (r *PolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	policy := &egv1a1.EvictionGuardPolicy{}
	if err := r.Get(ctx, req.NamespacedName, policy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !policy.DeletionTimestamp.IsZero() {
		return r.cleanup(ctx, policy)
	}

	if !controllerutil.ContainsFinalizer(policy, egv1a1.PolicyFinalizer) {
		patch := client.MergeFrom(policy.DeepCopy())
		controllerutil.AddFinalizer(policy, egv1a1.PolicyFinalizer)
		if err := r.Patch(ctx, policy, patch); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	filter, err := filters.FromSpec(policy.Spec.NodeFilter)
	if err != nil {
		return r.updateStatus(ctx, policy, 0, 0, 0, 0, err)
	}

	nodes := &corev1.NodeList{}
	if err := r.List(ctx, nodes); err != nil {
		return ctrl.Result{}, err
	}

	var matched, vulnerable []*corev1.Node
	for i := range nodes.Items {
		n := &nodes.Items[i]
		ok, err := filter.Matches(n)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !ok {
			continue
		}
		matched = append(matched, n)
		if signals.Vulnerable(n, policy) {
			vulnerable = append(vulnerable, n)
		}
	}

	metrics.MatchedNodes.WithLabelValues(policy.Name).Set(float64(len(matched)))
	metrics.VulnerableNodes.WithLabelValues(policy.Name).Set(float64(len(vulnerable)))

	workloads, err := r.collectWorkloads(ctx, policy, vulnerable)
	if err != nil {
		return ctrl.Result{}, err
	}

	active, err := r.countActiveWindows(ctx, policy)
	if err != nil {
		return ctrl.Result{}, err
	}
	cap := policy.MaxConcurrentWindowsOrDefault()
	var deferred int32
	for _, key := range sortedWorkloadKeys(workloads) {
		w := workloads[key]
		held, err := r.hasActiveWindow(ctx, policy, w)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !held && cap > 0 && active >= cap {
			deferred++
			continue
		}
		if err := r.ensureWindow(ctx, policy, w); err != nil {
			logger.Error(err, "scale-up failed", "deployment", w.deploy.Name, "namespace", w.deploy.Namespace)
			metrics.ScaleActions.WithLabelValues(policy.Name, "up", backendLabel(w.deploy, policy), "error").Inc()
			return ctrl.Result{}, err
		}
		if !held {
			active++
		}
	}
	if deferred > 0 && r.Recorder != nil {
		r.Recorder.Eventf(policy, corev1.EventTypeNormal, reasonWindowsCapped,
			"%d workload(s) waiting for a window slot (active %d, maxConcurrentWindows %d)",
			deferred, active, cap)
	}
	metrics.DeferredWorkloads.WithLabelValues(policy.Name).Set(float64(deferred))

	if err := r.syncClearedWindows(ctx, policy, workloads); err != nil {
		return ctrl.Result{}, err
	}

	active, err = r.countActiveWindows(ctx, policy)
	if err != nil {
		return ctrl.Result{}, err
	}

	return r.updateStatus(ctx, policy, int32(len(matched)), int32(len(vulnerable)), active, deferred, nil)
}

type workloadState struct {
	deploy *appsv1.Deployment
	atRisk int32
	nodes  map[string]struct{}
}

func (r *PolicyReconciler) collectWorkloads(ctx context.Context, policy *egv1a1.EvictionGuardPolicy, vulnerable []*corev1.Node) (map[string]*workloadState, error) {
	out := map[string]*workloadState{}
	for _, n := range vulnerable {
		pods, err := r.podsOnNode(ctx, n.Name)
		if err != nil {
			return nil, err
		}
		for i := range pods {
			pod := &pods[i]
			allowed, err := r.namespaceAllowed(ctx, policy, pod.Namespace)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
			dep, err := r.ownerDeployment(ctx, pod)
			if err != nil || dep == nil {
				continue
			}
			if !workloadSelected(policy, dep) {
				continue
			}
			if policy.RequirePDBOrDefault() {
				ok, err := r.hasPDB(ctx, dep)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
			}
			key := dep.Namespace + "/" + dep.Name
			st, ok := out[key]
			if !ok {
				st = &workloadState{deploy: dep, nodes: map[string]struct{}{}}
				out[key] = st
			}
			st.atRisk++
			st.nodes[n.Name] = struct{}{}
		}
	}
	return out, nil
}

func (r *PolicyReconciler) hasActiveWindow(ctx context.Context, policy *egv1a1.EvictionGuardPolicy, w *workloadState) (bool, error) {
	win := &egv1a1.EvictionGuardWindow{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: w.deploy.Namespace,
		Name:      WindowName(policy.Name, w.deploy.Namespace, w.deploy.Name),
	}, win)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return win.IsActive(), nil
}

func sortedWorkloadKeys(m map[string]*workloadState) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (r *PolicyReconciler) ensureWindow(ctx context.Context, policy *egv1a1.EvictionGuardPolicy, w *workloadState) error {
	winName := WindowName(policy.Name, w.deploy.Namespace, w.deploy.Name)
	win := &egv1a1.EvictionGuardWindow{}
	getErr := r.Get(ctx, types.NamespacedName{Namespace: w.deploy.Namespace, Name: winName}, win)
	exists := getErr == nil
	if !exists && !apierrors.IsNotFound(getErr) {
		return getErr
	}
	if exists && holdMaxWindow(policy, win, r.now()) {
		return nil
	}
	existingActions := []egv1a1.ScaleAction{}
	if exists {
		existingActions = win.ScaleActions()
	}

	plan, err := buildPlan(ctx, r.Client, policy, w.deploy, w.atRisk, existingActions)
	if err != nil {
		return err
	}
	scaled, err := plan.apply(ctx, r.Client, policy.Name)
	if err != nil {
		return err
	}

	nodeNames := sortedKeys(w.nodes)
	now := r.now()
	if scaled && r.Recorder != nil {
		r.Recorder.Eventf(w.deploy, corev1.EventTypeNormal, reasonScaledUp,
			"policy %q scaled replicas to %d (baseline %d) ahead of disruption on nodes %v",
			policy.Name, plan.desired, plan.primaryBaseline, nodeNames)
	}
	metrics.CurrentSpare.WithLabelValues(policy.Name, w.deploy.Namespace, w.deploy.Name).Set(float64(plan.desired - plan.primaryBaseline))

	actions := plan.actions(existingActions)
	prim := plan.primary()
	primaryAction := scaleAction(prim.backend, prim.target, prim.baseline, prim.desired, prim.stampKeys)

	if !exists {
		win = &egv1a1.EvictionGuardWindow{
			ObjectMeta: metav1.ObjectMeta{
				Name:      winName,
				Namespace: w.deploy.Namespace,
				Labels: map[string]string{
					egv1a1.PolicyLabel:            policy.Name,
					egv1a1.WorkloadNameLabel:      w.deploy.Name,
					egv1a1.WorkloadNamespaceLabel: w.deploy.Namespace,
				},
			},
			Spec: egv1a1.EvictionGuardWindowSpec{
				PolicyName:      policy.Name,
				Target:          workloadRef(w.deploy),
				Backend:         primaryAction.Backend,
				BackendTarget:   primaryBackendRef(w.deploy, primaryAction),
				Actions:         actions,
				Baseline:        plan.primaryBaseline,
				ScaledTo:        plan.desired,
				VulnerableNodes: nodeNames,
			},
		}
		controllerutil.AddFinalizer(win, egv1a1.WindowFinalizer)
		policy.SetGroupVersionKind(egv1a1.GroupVersion.WithKind("EvictionGuardPolicy"))
		if err := controllerutil.SetControllerReference(policy, win, r.Scheme); err != nil {
			return fmt.Errorf("ownerref: %w", err)
		}
		if err := r.Create(ctx, win); err != nil {
			return err
		}
		win.Status.Phase = egv1a1.WindowPhaseOpen
		win.Status.Message = "scaled up ahead of node disruption"
		ts := metav1.NewTime(now)
		win.Status.LastScaleTime = &ts
		if err := r.Status().Update(ctx, win); err != nil {
			return err
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(policy, corev1.EventTypeNormal, reasonWindowOpened,
				"opened window %s/%s for %s", win.Namespace, win.Name, w.deploy.Name)
		}
		return nil
	}

	patch := client.MergeFrom(win.DeepCopy())
	win.Spec.VulnerableNodes = nodeNames
	win.Spec.ScaledTo = plan.desired
	win.Spec.Actions = actions
	win.Spec.Backend = primaryAction.Backend
	win.Spec.BackendTarget = primaryBackendRef(w.deploy, primaryAction)
	win.Spec.WindowUntil = nil
	if err := r.Patch(ctx, win, patch); err != nil {
		return err
	}
	if win.Status.Phase != egv1a1.WindowPhaseOpen {
		win.Status.Phase = egv1a1.WindowPhaseOpen
		win.Status.Message = "vulnerable nodes present"
		if err := r.Status().Update(ctx, win); err != nil {
			return err
		}
	}
	return nil
}

func (r *PolicyReconciler) syncClearedWindows(ctx context.Context, policy *egv1a1.EvictionGuardPolicy, active map[string]*workloadState) error {
	list := &egv1a1.EvictionGuardWindowList{}
	if err := r.List(ctx, list, client.MatchingLabels{egv1a1.PolicyLabel: policy.Name}); err != nil {
		return err
	}
	for i := range list.Items {
		win := &list.Items[i]
		key := win.Spec.Target.Namespace + "/" + win.Spec.Target.Name
		if _, still := active[key]; still {
			continue
		}
		if len(win.Spec.VulnerableNodes) == 0 {
			continue
		}
		patch := client.MergeFrom(win.DeepCopy())
		win.Spec.VulnerableNodes = nil
		if err := r.Patch(ctx, win, patch); err != nil {
			return err
		}
	}
	return nil
}

func (r *PolicyReconciler) countActiveWindows(ctx context.Context, policy *egv1a1.EvictionGuardPolicy) (int32, error) {
	list := &egv1a1.EvictionGuardWindowList{}
	if err := r.List(ctx, list, client.MatchingLabels{egv1a1.PolicyLabel: policy.Name}); err != nil {
		return 0, err
	}
	var n int32
	for i := range list.Items {
		if list.Items[i].IsActive() {
			n++
		}
	}
	return n, nil
}

func (r *PolicyReconciler) cleanup(ctx context.Context, policy *egv1a1.EvictionGuardPolicy) (ctrl.Result, error) {
	list := &egv1a1.EvictionGuardWindowList{}
	if err := r.List(ctx, list, client.MatchingLabels{egv1a1.PolicyLabel: policy.Name}); err != nil {
		return ctrl.Result{}, err
	}
	if len(list.Items) > 0 {
		for i := range list.Items {
			if err := r.Delete(ctx, &list.Items[i]); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}
	if controllerutil.ContainsFinalizer(policy, egv1a1.PolicyFinalizer) {
		patch := client.MergeFrom(policy.DeepCopy())
		controllerutil.RemoveFinalizer(policy, egv1a1.PolicyFinalizer)
		if err := r.Patch(ctx, policy, patch); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *PolicyReconciler) updateStatus(ctx context.Context, policy *egv1a1.EvictionGuardPolicy, matched, vuln, windows, deferred int32, recErr error) (ctrl.Result, error) {
	policy.Status.ObservedGeneration = policy.Generation
	policy.Status.MatchedNodes = matched
	policy.Status.VulnerableNodes = vuln
	policy.Status.ActiveWindows = windows
	policy.Status.DeferredWorkloads = deferred
	msg := "watching filtered nodes"
	if deferred > 0 {
		msg = fmt.Sprintf("watching filtered nodes; %d workload(s) waiting for a window slot", deferred)
	}
	cond := metav1.Condition{
		Type:               reasonPolicyReady,
		Status:             metav1.ConditionTrue,
		Reason:             reasonPolicyReady,
		Message:            msg,
		LastTransitionTime: metav1.NewTime(r.now()),
	}
	if recErr != nil {
		cond.Status = metav1.ConditionFalse
		cond.Reason = reasonPolicyError
		cond.Message = recErr.Error()
	}
	policy.Status.Conditions = []metav1.Condition{cond}
	if err := r.Status().Update(ctx, policy); err != nil {
		if recErr != nil {
			return ctrl.Result{}, recErr
		}
		return ctrl.Result{}, err
	}
	if recErr != nil {
		return ctrl.Result{}, recErr
	}
	if deferred > 0 {
		return ctrl.Result{RequeueAfter: requeueDeferred}, nil
	}
	return ctrl.Result{}, nil
}

func (r *PolicyReconciler) podsOnNode(ctx context.Context, nodeName string) ([]corev1.Pod, error) {
	list := &corev1.PodList{}
	if err := r.List(ctx, list, client.MatchingFields{IndexPodNodeName: nodeName}); err == nil {
		return list.Items, nil
	}
	if err := r.List(ctx, list); err != nil {
		return nil, err
	}
	var out []corev1.Pod
	for i := range list.Items {
		if list.Items[i].Spec.NodeName == nodeName {
			out = append(out, list.Items[i])
		}
	}
	return out, nil
}

func (r *PolicyReconciler) namespaceAllowed(ctx context.Context, policy *egv1a1.EvictionGuardPolicy, ns string) (bool, error) {
	if policy.Spec.NamespaceSelector == nil {
		return true, nil
	}
	namespace := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: ns}, namespace); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	sel, err := metav1.LabelSelectorAsSelector(policy.Spec.NamespaceSelector)
	if err != nil {
		return false, err
	}
	return sel.Matches(labels.Set(namespace.Labels)), nil
}

func (r *PolicyReconciler) ownerDeployment(ctx context.Context, pod *corev1.Pod) (*appsv1.Deployment, error) {
	for _, o := range pod.OwnerReferences {
		if o.Kind != "ReplicaSet" || o.Controller == nil || !*o.Controller {
			continue
		}
		rs := &appsv1.ReplicaSet{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: o.Name}, rs); err != nil {
			return nil, client.IgnoreNotFound(err)
		}
		for _, oo := range rs.OwnerReferences {
			if oo.Kind != "Deployment" || oo.Controller == nil || !*oo.Controller {
				continue
			}
			dep := &appsv1.Deployment{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: oo.Name}, dep); err != nil {
				return nil, client.IgnoreNotFound(err)
			}
			return dep, nil
		}
	}
	return nil, nil
}

func (r *PolicyReconciler) hasPDB(ctx context.Context, dep *appsv1.Deployment) (bool, error) {
	list := &policyv1.PodDisruptionBudgetList{}
	if err := r.List(ctx, list, client.InNamespace(dep.Namespace)); err != nil {
		return false, err
	}
	podLabels := labels.Set(dep.Spec.Template.Labels)
	for i := range list.Items {
		pdb := &list.Items[i]
		if pdb.Spec.Selector == nil {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			continue
		}
		if sel.Matches(podLabels) {
			return true, nil
		}
	}
	return false, nil
}

func workloadSelected(policy *egv1a1.EvictionGuardPolicy, dep *appsv1.Deployment) bool {
	if policy.Spec.WorkloadSelector == nil {
		return dep.Labels[egv1a1.EnabledLabel] == "true"
	}
	sel, err := metav1.LabelSelectorAsSelector(policy.Spec.WorkloadSelector)
	if err != nil {
		return false
	}
	return sel.Matches(labels.Set(dep.Labels))
}

func workloadRef(dep *appsv1.Deployment) egv1a1.WorkloadReference {
	return egv1a1.WorkloadReference{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       dep.Name,
		Namespace:  dep.Namespace,
	}
}

// SetupWithManager watches policies, nodes, and pods.
// Node events enqueue policies whose nodeFilter matches the node (old or new,
// so a label change that leaves a filter still reconciles).
// Pod events enqueue only policies whose nodeFilter matches the pod's node
// and that node is currently vulnerable, and only when membership can change
// (bind, labels, delete) — not Ready/status flips.
func (r *PolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, IndexPodNodeName, func(o client.Object) []string {
		name := o.(*corev1.Pod).Spec.NodeName
		if name == "" {
			return nil
		}
		return []string{name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&egv1a1.EvictionGuardPolicy{}).
		Owns(&egv1a1.EvictionGuardWindow{}).
		Watches(&corev1.Node{}, nodePolicyHandler{Client: r.Client}, builder.WithPredicates(nodeDisruptionPredicate())).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.mapPodToPolicies), builder.WithPredicates(podPolicyPredicate())).
		Complete(r)
}

func nodeDisruptionPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			_, ok := e.Object.(*corev1.Node)
			return ok
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldN, ok1 := e.ObjectOld.(*corev1.Node)
			newN, ok2 := e.ObjectNew.(*corev1.Node)
			if !ok1 || !ok2 {
				return false
			}
			return taintsOrLabelsChanged(oldN, newN)
		},
		DeleteFunc: func(event.DeleteEvent) bool { return true },
	}
}

// podPolicyPredicate ignores Ready/status noise. The policy controller only
// needs to know that a pod appeared on, left, or was relabeled on a node.
func podPolicyPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			_, ok := e.Object.(*corev1.Pod)
			return ok
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldP, ok1 := e.ObjectOld.(*corev1.Pod)
			newP, ok2 := e.ObjectNew.(*corev1.Pod)
			if !ok1 || !ok2 {
				return false
			}
			return podMembershipChanged(oldP, newP)
		},
		DeleteFunc: func(event.DeleteEvent) bool { return true },
	}
}

func podMembershipChanged(a, b *corev1.Pod) bool {
	if a.Spec.NodeName != b.Spec.NodeName {
		return true
	}
	if len(a.Labels) != len(b.Labels) {
		return true
	}
	for k, v := range a.Labels {
		if b.Labels[k] != v {
			return true
		}
	}
	return deletionTimestampSet(a, b)
}

func deletionTimestampSet(a, b metav1.Object) bool {
	oldTS, newTS := a.GetDeletionTimestamp(), b.GetDeletionTimestamp()
	switch {
	case oldTS == nil && newTS != nil:
		return true
	case oldTS != nil && newTS == nil:
		return true
	default:
		return false
	}
}

func taintsOrLabelsChanged(a, b *corev1.Node) bool {
	if a.Spec.Unschedulable != b.Spec.Unschedulable {
		return true
	}
	if len(a.Spec.Taints) != len(b.Spec.Taints) {
		return true
	}
	for i := range a.Spec.Taints {
		if a.Spec.Taints[i] != b.Spec.Taints[i] {
			return true
		}
	}
	if len(a.Labels) != len(b.Labels) {
		return true
	}
	for k, v := range a.Labels {
		if b.Labels[k] != v {
			return true
		}
	}
	if len(a.Annotations) != len(b.Annotations) {
		return true
	}
	for k, v := range a.Annotations {
		if b.Annotations[k] != v {
			return true
		}
	}
	return false
}
