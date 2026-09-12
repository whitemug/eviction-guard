/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/recorder"

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
	msgNodesVulnerable = "at-risk pods still on vulnerable nodes"
	msgWaitingSpare    = "waiting for Ready pods off vulnerable nodes"
	msgMaxWindow       = "maxWindow exceeded; forcing cooldown"
)

// WindowReconciler closes disruption windows: SpareReady, cooldown, then scale-back.
type WindowReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder recorder.EventRecorder
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
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;update;patch

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

	// Target Deployment deleted: restore remaining backends (e.g. HPA) and close.
	// Without this, liveAtRisk/spareCounts treat NotFound as zeros and the window
	// stays Open waiting for spare until maxWindow.
	if k := win.Spec.Target.Kind; k == "" || k == "Deployment" {
		dep := &appsv1.Deployment{}
		tErr := r.Get(ctx, types.NamespacedName{
			Namespace: win.Spec.Target.Namespace, Name: win.Spec.Target.Name,
		}, dep)
		if apierrors.IsNotFound(tErr) {
			logger.Info("target gone; scaling back")
			return r.scaleBackAndClose(ctx, win)
		}
		if tErr != nil {
			return ctrl.Result{}, tErr
		}
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

	liveAtRisk, _, err := liveAtRiskPods(ctx, r.Client, win, policy)
	if err != nil {
		return ctrl.Result{}, err
	}
	now := r.now()
	forceCool := maxWindowExceeded(policy, win, now)
	// Stamp ForcedCool early so the eviction webhook can fail-open while we cool.
	if forceCool && !win.Status.ForcedCool {
		win.Status.ForcedCool = true
		emitf(r.Recorder, win, corev1.EventTypeWarning, reasonMaxWindow,
			"Open longer than maxWindow (%s); forcing cooldown", policy.MaxWindowOrDefault())
		metrics.MaxWindowExceeded.WithLabelValues(policy.Name, win.Namespace, win.Spec.Target.Name).Inc()
		if err := r.Status().Update(ctx, win); err != nil {
			return ctrl.Result{}, err
		}
	}

	safeReady := win.Spec.Baseline
	if !forceCool && liveAtRisk == 0 {
		_, safe, err := r.spareCounts(ctx, win)
		if err != nil {
			return ctrl.Result{}, err
		}
		safeReady = safe
	}
	var until time.Time
	if win.Spec.WindowUntil != nil {
		until = win.Spec.WindowUntil.Time
	}
	dec := decideWindowStep(windowPhaseInput{
		LiveAtRisk:  liveAtRisk,
		ForceCool:   forceCool,
		ForcedCool:  win.Status.ForcedCool,
		SafeReady:   safeReady,
		Baseline:    win.Spec.Baseline,
		WindowUntil: until,
		Now:         now,
	})

	switch dec.Step {
	case stepStayOpenVulnerable, stepStayOpenWaitingSpare:
		if dec.AbortCooldown {
			if err := r.abortCooldown(ctx, win, policy); err != nil {
				return ctrl.Result{}, err
			}
		}
		if err := r.writeStatus(ctx, win, dec.Phase, dec.Message); err != nil {
			return ctrl.Result{}, err
		}
		return openRequeue(policy, win, now), nil

	case stepBeginScaleBack:
		if err := r.scaleBackIfAllowed(ctx, win); err != nil {
			return ctrl.Result{}, err
		}
		dep, err := workloadDeployment(ctx, r.Client, win.Spec.Target.Namespace, win.Spec.Target.Name)
		if err != nil {
			return ctrl.Result{}, err
		}
		coolUntil := metav1.NewTime(now.Add(scaleBackAfter(policy, dep)))
		patch := client.MergeFrom(win.DeepCopy())
		win.Spec.WindowUntil = &coolUntil
		if err := r.Patch(ctx, win, patch); err != nil {
			return ctrl.Result{}, err
		}
		if err := stampCooldown(ctx, r.Client, policy, dep, win, coolUntil.Time); err != nil {
			return ctrl.Result{}, err
		}
		coolMsg := coolingMessage(forceCool, win.Status.ForcedCool)
		if err := r.writeStatus(ctx, win, egv1a1.WindowPhaseCooling, coolMsg); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: coolUntil.Sub(now)}, nil

	case stepStayCooling:
		if err := r.writeStatus(ctx, win, dec.Phase, dec.Message); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: until.Sub(now)}, nil

	case stepClose:
		if err := r.writeStatus(ctx, win, dec.Phase, dec.Message); err != nil {
			return ctrl.Result{}, err
		}
		if dec.RetainClosed {
			return ctrl.Result{}, nil
		}
		return r.deleteClosedWindow(ctx, win)
	}
	return ctrl.Result{}, nil
}

func openRequeue(policy *egv1a1.EvictionGuardPolicy, win *egv1a1.EvictionGuardWindow, now time.Time) ctrl.Result {
	res := ctrl.Result{RequeueAfter: 15 * time.Second}
	if d := maxWindowRequeue(policy, win, now); d > 0 && d < res.RequeueAfter {
		res.RequeueAfter = d
	}
	return res
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

func (r *WindowReconciler) scaleBackAndUnfinalize(ctx context.Context, win *egv1a1.EvictionGuardWindow) (ctrl.Result, error) {
	if err := restoreActions(ctx, r.Client, win); err != nil && !apierrors.IsNotFound(err) {
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

func (r *WindowReconciler) scaleBackIfAllowed(ctx context.Context, win *egv1a1.EvictionGuardWindow) error {
	if err := restoreActions(ctx, r.Client, win); err != nil {
		metrics.ScaleActions.WithLabelValues(win.Spec.PolicyName, "down", windowMetricBackend(win), "error").Inc()
		return err
	}
	metrics.ScaleActions.WithLabelValues(win.Spec.PolicyName, "down", windowMetricBackend(win), "ok").Inc()
	clearWorkloadPlanMetrics(win.Spec.PolicyName, win.Spec.Target.Namespace, win.Spec.Target.Name)
	emitf(r.Recorder, win, corev1.EventTypeNormal, reasonScaledBack, "restored capacity to baseline %d", win.Spec.Baseline)
	return nil
}

func (r *WindowReconciler) scaleBackAndClose(ctx context.Context, win *egv1a1.EvictionGuardWindow) (ctrl.Result, error) {
	if err := restoreActions(ctx, r.Client, win); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	return r.scaleBackAndUnfinalize(ctx, win)
}

func (r *WindowReconciler) writeStatus(ctx context.Context, win *egv1a1.EvictionGuardWindow, phase egv1a1.WindowPhase, msg string) error {
	ready, safe, err := r.spareCounts(ctx, win)
	if err != nil {
		return err
	}
	now := metav1.NewTime(r.now())
	oldReady, oldSafe := win.Status.ReadyReplicas, win.Status.SafeReadyReplicas
	before := win.Status.SpareReady
	transitioned := applySpareStatus(win, ready, safe, now)
	blocked := capacityAppliedBlocked(win)
	if blocked && (msg == msgWaitingSpare || msg == msgNodesVulnerable) {
		msg = applyErrorMessage(msg, win)
	}
	if transitioned {
		if win.Status.SpareReady && !before {
			emitf(r.Recorder, win, corev1.EventTypeNormal, reasonSpareReady,
				"spare capacity Ready off vulnerable nodes (safeReady=%d baseline=%d)", safe, win.Spec.Baseline)
		}
		if !win.Status.SpareReady {
			emitf(r.Recorder, win, corev1.EventTypeNormal, reasonSpareWait,
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

func windowMetricBackend(win *egv1a1.EvictionGuardWindow) string {
	actions := win.ScaleActions()
	if len(actions) == 0 {
		return "unknown"
	}
	parts := make([]string, 0, len(actions))
	for _, a := range actions {
		if a.Key != "" {
			parts = append(parts, a.Key)
			continue
		}
		parts = append(parts, a.Kind)
	}
	return strings.Join(parts, ",")
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
		Complete(r)
}
