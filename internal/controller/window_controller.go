/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/filters"
	"github.com/whitemug/eviction-guard/pkg/metrics"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

const (
	reasonScaledBack   = "ScaledBack"
	reasonCooling      = "CoolingDown"
	reasonSpareReady   = "SpareReady"
	reasonSpareWait    = "SpareNotReady"
	reasonMaxWindow    = "MaxWindowExceeded"
	msgNodesVulnerable = "nodes still vulnerable"
	msgWaitingSpare    = "waiting for Ready pods off vulnerable nodes"
	msgMaxWindow       = "maxWindow exceeded; forcing cooldown"
)

// WindowReconciler closes disruption windows: SpareReady, cooldown, HPA-aware floor, then scale-back.
type WindowReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Now      func() time.Time
}

func (r *WindowReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// +kubebuilder:rbac:groups=eviction-guard.io,resources=evictionguardwindows,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=eviction-guard.io,resources=evictionguardwindows/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=eviction-guard.io,resources=evictionguardwindows/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

func (r *WindowReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	win := &egv1a1.EvictionGuardWindow{}
	if err := r.Get(ctx, req.NamespacedName, win); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !win.DeletionTimestamp.IsZero() {
		return r.scaleBackAndUnfinalize(ctx, win)
	}

	if !controllerutil.ContainsFinalizer(win, egv1a1.WindowFinalizer) {
		patch := client.MergeFrom(win.DeepCopy())
		controllerutil.AddFinalizer(win, egv1a1.WindowFinalizer)
		return ctrl.Result{}, r.Patch(ctx, win, patch)
	}

	policy := &egv1a1.EvictionGuardPolicy{}
	polErr := r.Get(ctx, types.NamespacedName{Name: win.Spec.PolicyName}, policy)
	if apierrors.IsNotFound(polErr) {
		logger.Info("policy gone; scaling back")
		return r.scaleBackAndClose(ctx, win)
	}
	if polErr != nil {
		return ctrl.Result{}, polErr
	}

	if win.Status.Phase == egv1a1.WindowPhaseClosed && win.Status.ForcedCool {
		still, err := r.stillVulnerable(ctx, win, policy)
		if err != nil {
			return ctrl.Result{}, err
		}
		if len(still) > 0 {
			return ctrl.Result{}, nil
		}
		return r.deleteClosedWindow(ctx, win)
	}

	still, err := r.stillVulnerable(ctx, win, policy)
	if err != nil {
		return ctrl.Result{}, err
	}
	now := r.now()
	forceCool := maxWindowExceeded(policy, win, now)
	if len(still) > 0 && !forceCool {
		if err := r.abortCooldown(ctx, win, policy); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.writeStatus(ctx, win, egv1a1.WindowPhaseOpen, msgNodesVulnerable); err != nil {
			return ctrl.Result{}, err
		}
		res := ctrl.Result{}
		if !win.Status.SpareReady {
			res.RequeueAfter = 15 * time.Second
		}
		if d := maxWindowRequeue(policy, win, now); d > 0 && (res.RequeueAfter == 0 || d < res.RequeueAfter) {
			res.RequeueAfter = d
		}
		return res, nil
	}

	if !forceCool {
		_, safe, err := r.spareCounts(ctx, win)
		if err != nil {
			return ctrl.Result{}, err
		}
		if safe < win.Spec.Baseline {
			if err := r.abortCooldown(ctx, win, policy); err != nil {
				return ctrl.Result{}, err
			}
			if err := r.writeStatus(ctx, win, egv1a1.WindowPhaseOpen, msgWaitingSpare); err != nil {
				return ctrl.Result{}, err
			}
			res := ctrl.Result{RequeueAfter: 15 * time.Second}
			if d := maxWindowRequeue(policy, win, now); d > 0 && d < res.RequeueAfter {
				res.RequeueAfter = d
			}
			return res, nil
		}
	}
	if win.Spec.WindowUntil == nil || win.Spec.WindowUntil.Time.IsZero() {
		dep, err := workloadDeployment(ctx, r.Client, win.Spec.Target.Namespace, win.Spec.Target.Name)
		if err != nil {
			return ctrl.Result{}, err
		}
		until := metav1.NewTime(now.Add(scaleBackAfter(policy, dep)))
		patch := client.MergeFrom(win.DeepCopy())
		win.Spec.WindowUntil = &until
		if err := r.Patch(ctx, win, patch); err != nil {
			return ctrl.Result{}, err
		}
		if err := stampCooldown(ctx, r.Client, policy, dep, win, until.Time); err != nil {
			return ctrl.Result{}, err
		}
		coolMsg := reasonCooling
		if forceCool {
			if !win.Status.ForcedCool {
				win.Status.ForcedCool = true
				if r.Recorder != nil {
					r.Recorder.Eventf(win, corev1.EventTypeWarning, reasonMaxWindow,
						"Open longer than maxWindow (%s); forcing cooldown", policy.MaxWindowOrDefault())
				}
				metrics.MaxWindowExceeded.WithLabelValues(policy.Name, win.Namespace, win.Spec.Target.Name).Inc()
			}
			coolMsg = msgMaxWindow
		}
		if err := r.writeStatus(ctx, win, egv1a1.WindowPhaseCooling, coolMsg); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: until.Sub(now)}, nil
	}
	if now.Before(win.Spec.WindowUntil.Time) {
		coolMsg := reasonCooling
		if forceCool || win.Status.ForcedCool {
			coolMsg = msgMaxWindow
		}
		if err := r.writeStatus(ctx, win, egv1a1.WindowPhaseCooling, coolMsg); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: win.Spec.WindowUntil.Sub(now)}, nil
	}

	floor, hold, err := r.scaleFloor(ctx, win)
	if err != nil {
		return ctrl.Result{}, err
	}
	if hold {
		if err := r.writeStatus(ctx, win, egv1a1.WindowPhaseHeld, "HPA is holding or raising capacity; not fighting"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	if err := restoreActions(ctx, r.Client, win, floor); err != nil {
		metrics.ScaleActions.WithLabelValues(win.Spec.PolicyName, "down", string(win.Spec.Backend), "error").Inc()
		return ctrl.Result{}, err
	}
	metrics.ScaleActions.WithLabelValues(win.Spec.PolicyName, "down", string(win.Spec.Backend), "ok").Inc()
	metrics.CurrentSpare.WithLabelValues(win.Spec.PolicyName, win.Spec.Target.Namespace, win.Spec.Target.Name).Set(0)
	metrics.SpareNotReady.WithLabelValues(win.Spec.PolicyName, win.Spec.Target.Namespace, win.Spec.Target.Name).Set(0)

	if r.Recorder != nil {
		r.Recorder.Eventf(win, corev1.EventTypeNormal, reasonScaledBack, "restored capacity to %d", floor)
	}

	closedMsg := "scaled back to baseline"
	if win.Status.ForcedCool {
		closedMsg = "scaled back after maxWindow; holding until nodes clear"
	}
	if err := r.writeStatus(ctx, win, egv1a1.WindowPhaseClosed, closedMsg); err != nil {
		return ctrl.Result{}, err
	}
	if win.Status.ForcedCool {
		return ctrl.Result{}, nil
	}
	return r.deleteClosedWindow(ctx, win)
}

func (r *WindowReconciler) deleteClosedWindow(ctx context.Context, win *egv1a1.EvictionGuardWindow) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(win, egv1a1.WindowFinalizer) {
		patch := client.MergeFrom(win.DeepCopy())
		controllerutil.RemoveFinalizer(win, egv1a1.WindowFinalizer)
		if err := r.Patch(ctx, win, patch); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := r.Delete(ctx, win); client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *WindowReconciler) stillVulnerable(ctx context.Context, win *egv1a1.EvictionGuardWindow, policy *egv1a1.EvictionGuardPolicy) ([]string, error) {
	filter, err := filters.FromSpec(policy.Spec.NodeFilter)
	if err != nil {
		return nil, err
	}
	var still []string
	for _, name := range win.Spec.VulnerableNodes {
		n := &corev1.Node{}
		if err := r.Get(ctx, types.NamespacedName{Name: name}, n); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		ok, err := filter.Matches(n)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if signals.Vulnerable(n, policy) {
			still = append(still, name)
		}
	}
	return still, nil
}

func (r *WindowReconciler) abortCooldown(ctx context.Context, win *egv1a1.EvictionGuardWindow, policy *egv1a1.EvictionGuardPolicy) error {
	if win.Spec.WindowUntil == nil {
		return nil
	}
	patch := client.MergeFrom(win.DeepCopy())
	win.Spec.WindowUntil = nil
	if err := r.Patch(ctx, win, patch); err != nil {
		return err
	}
	dep, err := workloadDeployment(ctx, r.Client, win.Spec.Target.Namespace, win.Spec.Target.Name)
	if err != nil {
		return err
	}
	return stampCooldown(ctx, r.Client, policy, dep, win, time.Time{})
}

func (r *WindowReconciler) scaleFloor(ctx context.Context, win *egv1a1.EvictionGuardWindow) (int32, bool, error) {
	floor := win.Spec.Baseline
	hpa, err := r.hpaFor(ctx, win)
	if err != nil {
		return 0, false, err
	}
	if hpa == nil {
		return floor, false, nil
	}
	// Desired/current equal to ScaledTo is our own scale-up echoing through HPA — still scale back.
	// Hold only when HPA has moved *past* the spare we added (genuine load).
	if hpa.Status.DesiredReplicas > win.Spec.ScaledTo {
		return floor, true, nil
	}
	if hpa.Status.CurrentReplicas > win.Spec.ScaledTo {
		return floor, true, nil
	}
	return floor, false, nil
}

func (r *WindowReconciler) scaleBackAndUnfinalize(ctx context.Context, win *egv1a1.EvictionGuardWindow) (ctrl.Result, error) {
	if err := restoreActions(ctx, r.Client, win, win.Spec.Baseline); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	if controllerutil.ContainsFinalizer(win, egv1a1.WindowFinalizer) {
		patch := client.MergeFrom(win.DeepCopy())
		controllerutil.RemoveFinalizer(win, egv1a1.WindowFinalizer)
		if err := r.Patch(ctx, win, patch); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *WindowReconciler) scaleBackAndClose(ctx context.Context, win *egv1a1.EvictionGuardWindow) (ctrl.Result, error) {
	if err := restoreActions(ctx, r.Client, win, win.Spec.Baseline); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	return r.scaleBackAndUnfinalize(ctx, win)
}

func (r *WindowReconciler) writeStatus(ctx context.Context, win *egv1a1.EvictionGuardWindow, phase egv1a1.WindowPhase, msg string) error {
	ready, safe, err := r.spareCounts(ctx, win)
	if err != nil {
		return err
	}
	oldReady, oldSafe := win.Status.ReadyReplicas, win.Status.SafeReadyReplicas
	before := win.Status.SpareReady
	transitioned := applySpareStatus(win, ready, safe, metav1.NewTime(r.now()))
	if r.Recorder != nil && transitioned {
		if win.Status.SpareReady && !before {
			r.Recorder.Eventf(win, corev1.EventTypeNormal, reasonSpareReady,
				"spare capacity Ready off vulnerable nodes (safeReady=%d baseline=%d)", safe, win.Spec.Baseline)
		}
		if !win.Status.SpareReady {
			r.Recorder.Eventf(win, corev1.EventTypeNormal, reasonSpareWait,
				"waiting for Ready pods off vulnerable nodes (safeReady=%d baseline=%d)", safe, win.Spec.Baseline)
		}
	}
	if win.Status.Phase == phase && win.Status.Message == msg && !transitioned && oldReady == ready && oldSafe == safe {
		return nil
	}
	win.Status.Phase = phase
	win.Status.Message = msg
	return r.Status().Update(ctx, win)
}

func (r *WindowReconciler) hpaFor(ctx context.Context, win *egv1a1.EvictionGuardWindow) (*autoscalingv1.HorizontalPodAutoscaler, error) {
	for _, a := range win.ScaleActions() {
		if a.Backend != egv1a1.ScaleBackendHPAMin {
			continue
		}
		hpa := &autoscalingv1.HorizontalPodAutoscaler{}
		ns := a.Namespace
		if ns == "" {
			ns = win.Namespace
		}
		err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: a.Name}, hpa)
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return hpa, err
	}
	if win.Spec.Backend == egv1a1.ScaleBackendHPAMin && win.Spec.BackendTarget != nil && win.Spec.BackendTarget.Name != "" {
		hpa := &autoscalingv1.HorizontalPodAutoscaler{}
		ns := win.Spec.BackendTarget.Namespace
		if ns == "" {
			ns = win.Namespace
		}
		err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: win.Spec.BackendTarget.Name}, hpa)
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return hpa, err
	}
	list := &autoscalingv1.HorizontalPodAutoscalerList{}
	if err := r.List(ctx, list, client.InNamespace(win.Spec.Target.Namespace)); err != nil {
		return nil, err
	}
	for i := range list.Items {
		ref := list.Items[i].Spec.ScaleTargetRef
		if ref.Name == win.Spec.Target.Name && (ref.Kind == "" || ref.Kind == win.Spec.Target.Kind) {
			return &list.Items[i], nil
		}
	}
	return nil, nil
}

func (r *WindowReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mapNode := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		list := &egv1a1.EvictionGuardWindowList{}
		if err := r.List(ctx, list); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for i := range list.Items {
			w := &list.Items[i]
			if containsString(w.Spec.VulnerableNodes, obj.GetName()) {
				reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: w.Namespace, Name: w.Name}})
			}
		}
		return reqs
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&egv1a1.EvictionGuardWindow{}).
		Watches(&corev1.Node{}, mapNode).
		Watches(&appsv1.Deployment{}, handler.EnqueueRequestsFromMapFunc(r.workloadToWindows)).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.podToWindows)).
		Watches(&autoscalingv1.HorizontalPodAutoscaler{}, handler.EnqueueRequestsFromMapFunc(r.hpaToWindows)).
		Complete(r)
}

func (r *WindowReconciler) hpaToWindows(ctx context.Context, obj client.Object) []reconcile.Request {
	list := &egv1a1.EvictionGuardWindowList{}
	if err := r.List(ctx, list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	var reqs []reconcile.Request
	for i := range list.Items {
		w := &list.Items[i]
		if w.Spec.Target.Name == obj.GetName() || (w.Spec.BackendTarget != nil && w.Spec.BackendTarget.Name == obj.GetName()) {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: w.Namespace, Name: w.Name}})
			continue
		}
		for _, a := range w.ScaleActions() {
			if a.Backend == egv1a1.ScaleBackendHPAMin && a.Name == obj.GetName() {
				reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: w.Namespace, Name: w.Name}})
				break
			}
		}
	}
	return reqs
}
