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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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
	"github.com/whitemug/eviction-guard/pkg/policyown"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

const (
	IndexPodNodeName = "spec.nodeName"

	reasonScaledUp      = "ScaledUp"
	reasonPolicyReady   = "Ready"
	reasonPolicyError   = "ReconcileError"
	reasonWindowOpened  = "WindowOpened"
	reasonWindowsCapped = "WindowsCapped"
	reasonScaleUpFailed = "ScaleUpFailed"
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
		if _, uerr := r.updateStatus(ctx, policy, 0, 0, 0, 0, err); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{}, err
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
	var scaleErrs []error
	for _, key := range sortedWorkloadKeys(workloads) {
		w := workloads[key]
		held, err := r.hasActiveWindow(ctx, policy, w)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !held && cap > 0 && active >= cap {
			deferred++
			if err := r.publishWorkloadMetrics(ctx, policy, w); err != nil {
				logger.Error(err, "workload metrics", "deployment", w.deploy.Name, "namespace", w.deploy.Namespace)
			}
			continue
		}
		if err := r.ensureWindow(ctx, policy, w); err != nil {
			logger.Error(err, "scale-up failed", "deployment", w.deploy.Name, "namespace", w.deploy.Namespace)
			metrics.ScaleActions.WithLabelValues(policy.Name, "up", backendLabel(w.deploy, policy), "error").Inc()
			if r.Recorder != nil {
				r.Recorder.Eventf(w.deploy, corev1.EventTypeWarning, reasonScaleUpFailed, "%v", err)
				r.Recorder.Eventf(policy, corev1.EventTypeWarning, reasonScaleUpFailed,
					"scale-up %s/%s failed: %v", w.deploy.Namespace, w.deploy.Name, err)
			}
			scaleErrs = append(scaleErrs, fmt.Errorf("%s/%s: %w", w.deploy.Namespace, w.deploy.Name, err))
			continue
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

	var workloadErr error
	switch len(scaleErrs) {
	case 0:
	case 1:
		workloadErr = scaleErrs[0]
	default:
		workloadErr = fmt.Errorf("%d workloads failed scale-up; first: %w", len(scaleErrs), scaleErrs[0])
	}
	res, err := r.updateStatus(ctx, policy, int32(len(matched)), int32(len(vulnerable)), active, deferred, workloadErr)
	if err != nil {
		return res, err
	}
	if workloadErr != nil || deferred > 0 {
		return ctrl.Result{RequeueAfter: requeueDeferred}, nil
	}
	return res, nil
}

type workloadState struct {
	deploy *appsv1.Deployment
	atRisk int32
	nodes  map[string]struct{}
}

func (r *PolicyReconciler) collectWorkloads(ctx context.Context, policy *egv1a1.EvictionGuardPolicy, vulnerable []*corev1.Node) (map[string]*workloadState, error) {
	allList := &egv1a1.EvictionGuardPolicyList{}
	if err := r.List(ctx, allList); err != nil {
		return nil, err
	}
	all := make([]*egv1a1.EvictionGuardPolicy, 0, len(allList.Items))
	for i := range allList.Items {
		all = append(all, &allList.Items[i])
	}

	nsCache := map[string]labels.Set{}
	out := map[string]*workloadState{}
	for _, n := range vulnerable {
		pods, err := r.podsOnNode(ctx, n.Name)
		if err != nil {
			return nil, err
		}
		for i := range pods {
			pod := &pods[i]
			if pod.DeletionTimestamp != nil {
				continue
			}
			dep, err := r.ownerDeployment(ctx, pod)
			if err != nil || dep == nil {
				continue
			}
			if !workloadProtected(dep, pod) {
				continue
			}
			nsLabels, err := r.namespaceLabels(ctx, pod.Namespace, nsCache)
			if err != nil {
				return nil, err
			}
			if !policyown.Owns(policy, dep, nsLabels, all) {
				continue
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

func (r *PolicyReconciler) namespaceLabels(ctx context.Context, nsName string, cache map[string]labels.Set) (labels.Set, error) {
	if cached, ok := cache[nsName]; ok {
		return cached, nil
	}
	ns := &corev1.Namespace{}
	if err := r.Get(ctx, types.NamespacedName{Name: nsName}, ns); err != nil {
		if apierrors.IsNotFound(err) {
			cache[nsName] = labels.Set{}
			return labels.Set{}, nil
		}
		return nil, err
	}
	set := labels.Set(ns.Labels)
	cache[nsName] = set
	return set, nil
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
	stickyBaseline := int32(0)
	if exists {
		existingActions = win.ScaleActions()
		stickyBaseline = win.Spec.Baseline
	}

	nodeNames := sortedKeys(w.nodes)

	// Publish at-risk even if planning/patching fails (external scalers still need the signal).
	metrics.AtRiskPods.WithLabelValues(policy.Name, w.deploy.Namespace, w.deploy.Name).Set(float64(w.atRisk))

	plan, err := buildPlan(ctx, r.Client, policy, w.deploy, w.atRisk, existingActions, stickyBaseline)
	if err != nil {
		return err
	}
	setWorkloadPlanMetrics(policy.Name, w.deploy.Namespace, w.deploy.Name, w.atRisk, plan.desired, plan.primaryBaseline)

	scaled, err := plan.apply(ctx, r.Client, policy.Name)
	if err != nil {
		return err
	}

	now := r.now()
	if scaled && r.Recorder != nil {
		r.Recorder.Eventf(w.deploy, corev1.EventTypeNormal, reasonScaledUp,
			"policy %q scaled replicas to %d (baseline %d) ahead of disruption on nodes %v",
			policy.Name, plan.desired, plan.primaryBaseline, nodeNames)
	}
	actions := plan.actions(existingActions)
	openMsg := "scaled up ahead of node disruption"
	if len(plan.steps) == 0 {
		openMsg = "opened window for external scaler (no capacity patches)"
	}

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
		win.Status.Message = openMsg
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

func (r *PolicyReconciler) publishWorkloadMetrics(ctx context.Context, policy *egv1a1.EvictionGuardPolicy, w *workloadState) error {
	winName := WindowName(policy.Name, w.deploy.Namespace, w.deploy.Name)
	win := &egv1a1.EvictionGuardWindow{}
	existing := []egv1a1.ScaleAction{}
	stickyBaseline := int32(0)
	err := r.Get(ctx, types.NamespacedName{Namespace: w.deploy.Namespace, Name: winName}, win)
	if err == nil {
		existing = win.ScaleActions()
		stickyBaseline = win.Spec.Baseline
	} else if client.IgnoreNotFound(err) != nil {
		return err
	}
	plan, err := buildPlan(ctx, r.Client, policy, w.deploy, w.atRisk, existing, stickyBaseline)
	if err != nil {
		return err
	}
	setWorkloadPlanMetrics(policy.Name, w.deploy.Namespace, w.deploy.Name, w.atRisk, plan.desired, plan.primaryBaseline)
	return nil
}

func setWorkloadPlanMetrics(policy, ns, workload string, atRisk, desired, baseline int32) {
	metrics.AtRiskPods.WithLabelValues(policy, ns, workload).Set(float64(atRisk))
	metrics.DesiredReplicas.WithLabelValues(policy, ns, workload).Set(float64(desired))
	spare := desired - baseline
	if spare < 0 {
		spare = 0
	}
	metrics.CurrentSpare.WithLabelValues(policy, ns, workload).Set(float64(spare))
}

func clearWorkloadPlanMetrics(policy, ns, workload string) {
	metrics.AtRiskPods.WithLabelValues(policy, ns, workload).Set(0)
	metrics.DesiredReplicas.WithLabelValues(policy, ns, workload).Set(0)
	metrics.CurrentSpare.WithLabelValues(policy, ns, workload).Set(0)
	metrics.SpareNotReady.WithLabelValues(policy, ns, workload).Set(0)
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
		// No at-risk pods on vulnerable nodes right now (may still be waiting on node markers).
		metrics.AtRiskPods.WithLabelValues(policy.Name, win.Spec.Target.Namespace, win.Spec.Target.Name).Set(0)
		if len(win.Spec.VulnerableNodes) == 0 {
			continue
		}
		// Keep Spec while any recorded node still carries a disruption signal.
		// Clearing early (e.g. brief indexer miss on at-risk pods) makes the
		// window think at-risk is gone and flaps scale-back against the policy.
		dying, err := r.nodesStillVulnerable(ctx, policy, win.Spec.VulnerableNodes)
		if err != nil {
			return err
		}
		if dying {
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

// nodesStillVulnerable reports whether any named node still matches the policy
// filter and carries a disruption signal.
func (r *PolicyReconciler) nodesStillVulnerable(ctx context.Context, policy *egv1a1.EvictionGuardPolicy, names []string) (bool, error) {
	if len(names) == 0 {
		return false, nil
	}
	filter, err := filters.FromSpec(policy.Spec.NodeFilter)
	if err != nil {
		return false, err
	}
	for _, name := range names {
		n := &corev1.Node{}
		if err := r.Get(ctx, types.NamespacedName{Name: name}, n); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, err
		}
		ok, err := filter.Matches(n)
		if err != nil {
			return false, err
		}
		if ok && signals.Vulnerable(n, policy) {
			return true, nil
		}
	}
	return false, nil
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
	now := metav1.NewTime(r.now())
	msg := "watching filtered nodes"
	if deferred > 0 {
		msg = fmt.Sprintf("watching filtered nodes; %d workload(s) waiting for a window slot", deferred)
	}
	ready := metav1.Condition{
		Type:               reasonPolicyReady,
		Status:             metav1.ConditionTrue,
		Reason:             reasonPolicyReady,
		Message:            msg,
		LastTransitionTime: now,
	}
	if recErr != nil {
		ready.Status = metav1.ConditionFalse
		ready.Reason = reasonPolicyError
		ready.Message = recErr.Error()
	}
	meta.SetStatusCondition(&policy.Status.Conditions, ready)

	auth := metav1.Condition{
		Type:               egv1a1.ConditionBackendsAuthorized,
		Status:             metav1.ConditionTrue,
		Reason:             egv1a1.ReasonAuthorized,
		Message:            "catalog backend access ok",
		LastTransitionTime: now,
	}
	if isMissingBackendRBAC(recErr) {
		auth.Status = metav1.ConditionFalse
		auth.Reason = egv1a1.ReasonMissingRBAC
		auth.Message = recErr.Error()
	}
	meta.SetStatusCondition(&policy.Status.Conditions, auth)

	if err := r.Status().Update(ctx, policy); err != nil {
		return ctrl.Result{}, err
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

func workloadProtected(dep *appsv1.Deployment, pod *corev1.Pod) bool {
	if pod == nil || pod.Labels == nil || pod.Labels[egv1a1.ProtectedLabel] != "true" {
		return false
	}
	if dep == nil || dep.Spec.Template.Labels == nil || dep.Spec.Template.Labels[egv1a1.ProtectedLabel] != "true" {
		return false
	}
	return true
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
