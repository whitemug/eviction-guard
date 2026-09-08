/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/pkg/signals"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := egv1a1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func defaultCatalogBackends() map[string]egv1a1.BackendCatalogEntry {
	return map[string]egv1a1.BackendCatalogEntry{
		"deployment": {
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Patches:    []egv1a1.BackendPatch{{Path: "spec.replicas"}},
		},
		"hpa": {
			APIVersion: "autoscaling/v1",
			Kind:       "HorizontalPodAutoscaler",
			Patches:    []egv1a1.BackendPatch{{Path: "spec.minReplicas"}},
		},
	}
}

func fixture(t *testing.T, nodeLabels map[string]string, taints []corev1.Taint) (client.Client, *PolicyReconciler, *WindowReconciler) {
	t.Helper()
	scheme := testScheme(t)
	replicas := int32(3)
	trueVal := true
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "app",
			UID:       "dep-uid",
			Annotations: map[string]string{
				egv1a1.ScaleBackendAnnotation: "deployment",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
					"app":                 "web",
					egv1a1.ProtectedLabel: "true",
				}},
			},
		},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-rs",
			Namespace: "app",
			UID:       "rs-uid",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "web", UID: "dep-uid", Controller: &trueVal,
			}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-pod",
			Namespace: "app",
			Labels: map[string]string{
				"app":                 "web",
				egv1a1.ProtectedLabel: "true",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-rs", UID: "rs-uid", Controller: &trueVal,
			}},
		},
		Spec: corev1.PodSpec{NodeName: "worker-1"},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Labels: nodeLabels},
		Spec:       corev1.NodeSpec{Taints: taints},
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "app"}}
	policy := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "spot-workers", UID: "policy-uid", Finalizers: []string{egv1a1.PolicyFinalizer}},
		Spec: egv1a1.EvictionGuardPolicySpec{
			NodeFilter: egv1a1.NodeFilter{
				CapacityTypes: []string{"spot"},
			},
			SpareReplicas: ptr.To(int32(1)),
			MaxBuffer:     ptr.To(int32(4)),
			Backends:      defaultCatalogBackends(),
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&egv1a1.EvictionGuardPolicy{}, &egv1a1.EvictionGuardWindow{}, &autoscalingv1.HorizontalPodAutoscaler{}).
		WithIndex(&corev1.Pod{}, IndexPodNodeName, func(o client.Object) []string {
			n := o.(*corev1.Pod).Spec.NodeName
			if n == "" {
				return nil
			}
			return []string{n}
		}).
		WithObjects(ns, dep, rs, pod, node, policy).
		Build()

	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	pr := &PolicyReconciler{Client: c, Scheme: scheme, Recorder: events.NewFakeRecorder(16), Now: func() time.Time { return now }}
	wr := &WindowReconciler{Client: c, Scheme: scheme, Recorder: events.NewFakeRecorder(16), Now: func() time.Time { return now }}
	return c, pr, wr
}

func putSafeReadyPods(t *testing.T, c client.Client, n int) {
	t.Helper()
	trueVal := true
	for i := 0; i < n; i++ {
		p := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "web-safe-" + string(rune('a'+i)),
				Namespace: "app",
				Labels: map[string]string{
					"app":                 "web",
					egv1a1.ProtectedLabel: "true",
				},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "web-rs", UID: "rs-uid", Controller: &trueVal,
				}},
			},
			Spec: corev1.PodSpec{NodeName: "worker-2"},
			Status: corev1.PodStatus{
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			},
		}
		if err := c.Create(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPolicyIgnoresUnprotectedPods(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	pod := &corev1.Pod{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web-pod"}, pod); err != nil {
		t.Fatal(err)
	}
	delete(pod.Labels, egv1a1.ProtectedLabel)
	if err := c.Update(ctx, pod); err != nil {
		t.Fatal(err)
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	delete(dep.Spec.Template.Labels, egv1a1.ProtectedLabel)
	if err := c.Update(ctx, dep); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if *dep.Spec.Replicas != 3 {
		t.Fatalf("replicas=%d, want 3 (pod not protected)", *dep.Spec.Replicas)
	}
}

func TestPolicyIgnoresNodesOutsideFilter(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "on-demand"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	if _, err := pr.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if *dep.Spec.Replicas != 3 {
		t.Fatalf("replicas=%d, want 3 (node filtered out)", *dep.Spec.Replicas)
	}
	list := &egv1a1.EvictionGuardWindowList{}
	if err := c.List(context.Background(), list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("unexpected windows: %d", len(list.Items))
	}
}

func TestPolicyScalesUpMatchingVulnerableNode(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	if _, err := pr.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if *dep.Spec.Replicas != 4 {
		t.Fatalf("replicas=%d, want 4", *dep.Spec.Replicas)
	}
	winName := WindowName("spot-workers", "app", "web")
	win := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "app", Name: winName}, win); err != nil {
		t.Fatal(err)
	}
	if win.Spec.Baseline != 3 || win.Spec.ScaledTo != 4 {
		t.Fatalf("window baseline=%d scaledTo=%d", win.Spec.Baseline, win.Spec.ScaledTo)
	}
	if len(win.Spec.Actions) != 1 || win.Spec.Actions[0].Kind != "Deployment" || win.Spec.Actions[0].Key != "deployment" {
		t.Fatalf("actions=%+v", win.Spec.Actions)
	}
	if win.Status.Phase != egv1a1.WindowPhaseOpen {
		t.Fatalf("phase=%s", win.Status.Phase)
	}
	if win.Spec.WindowUntil != nil {
		t.Fatalf("windowUntil=%v, want unset while Open", win.Spec.WindowUntil)
	}
}

func TestWindowScaleBackAfterCooldown(t *testing.T) {
	c, pr, wr := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}

	node := &corev1.Node{}
	if err := c.Get(ctx, types.NamespacedName{Name: "worker-1"}, node); err != nil {
		t.Fatal(err)
	}
	node.Spec.Taints = nil
	if err := c.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}

	winName := WindowName("spot-workers", "app", "web")
	win := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: winName}, win); err != nil {
		t.Fatal(err)
	}
	if win.Spec.WindowUntil != nil {
		t.Fatalf("windowUntil=%v, want unset until the window controller arms cooldown", win.Spec.WindowUntil)
	}

	putSafeReadyPods(t, c, 3)

	fixed := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	wr.Now = func() time.Time { return fixed }
	res, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "app", Name: winName}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: winName}, win); err != nil {
		t.Fatal(err)
	}
	wantUntil := time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC)
	if win.Spec.WindowUntil == nil || !win.Spec.WindowUntil.Time.Equal(wantUntil) {
		t.Fatalf("windowUntil=%v, want %s after cooldown starts", win.Spec.WindowUntil, wantUntil)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("expected cooldown requeue, got %+v", res)
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if *dep.Spec.Replicas != 3 {
		t.Fatalf("replicas=%d, want 3 at cooldown start (do not wait to spawn a replacement)", *dep.Spec.Replicas)
	}

	wr.Now = func() time.Time { return fixed.Add(20 * time.Minute) }
	if _, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "app", Name: winName}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: winName}, win); err == nil {
		t.Fatal("expected window deleted after cooldown")
	}
}

func TestWindowScalesBackWhenAtRiskPodGoneNodeStillTainted(t *testing.T) {
	c, pr, wr := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	putSafeReadyPods(t, c, 3)
	if err := c.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-pod", Namespace: "app"}}); err != nil {
		t.Fatal(err)
	}

	winName := types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}
	if _, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: winName}); err != nil {
		t.Fatal(err)
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if *dep.Spec.Replicas != 3 {
		t.Fatalf("replicas=%d, want 3 after at-risk pod gone (node still tainted)", *dep.Spec.Replicas)
	}
	win := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(ctx, winName, win); err != nil {
		t.Fatal(err)
	}
	if win.Status.Phase != egv1a1.WindowPhaseCooling {
		t.Fatalf("phase=%s, want Cooling", win.Status.Phase)
	}
}

func TestSpareReadyRequiresPodsOffVulnerableNode(t *testing.T) {
	c, pr, wr := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	winName := WindowName("spot-workers", "app", "web")
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "app", Name: winName}}
	if _, err := wr.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	win := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(ctx, req.NamespacedName, win); err != nil {
		t.Fatal(err)
	}
	if win.Status.SpareReady {
		t.Fatal("spare must not be Ready while the only pod is on the vulnerable node")
	}
	if win.Spec.WindowUntil != nil {
		t.Fatal("cooldown must not start while nodes are still vulnerable")
	}

	putSafeReadyPods(t, c, 3)
	if _, err := wr.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, req.NamespacedName, win); err != nil {
		t.Fatal(err)
	}
	if !win.Status.SpareReady {
		t.Fatalf("expected SpareReady, got ready=%d safe=%d", win.Status.ReadyReplicas, win.Status.SafeReadyReplicas)
	}
	if win.Spec.WindowUntil != nil {
		t.Fatal("cooldown must not start until vulnerable nodes have also cleared")
	}
}

func TestWindowWaitsForSpareReadyBeforeCooldown(t *testing.T) {
	c, pr, wr := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	node := &corev1.Node{}
	if err := c.Get(ctx, types.NamespacedName{Name: "worker-1"}, node); err != nil {
		t.Fatal(err)
	}
	node.Spec.Taints = nil
	if err := c.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}

	winName := types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}
	res, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: winName})
	if err != nil {
		t.Fatal(err)
	}
	win := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(ctx, winName, win); err != nil {
		t.Fatal(err)
	}
	if win.Spec.WindowUntil != nil {
		t.Fatalf("cooldown started before SpareReady: until=%v", win.Spec.WindowUntil)
	}
	if win.Status.Phase != egv1a1.WindowPhaseOpen {
		t.Fatalf("phase=%s, want Open while waiting for spare", win.Status.Phase)
	}
	if res.RequeueAfter != 15*time.Second {
		t.Fatalf("requeue=%s, want 15s", res.RequeueAfter)
	}

	putSafeReadyPods(t, c, 3)
	if _, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: winName}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, winName, win); err != nil {
		t.Fatal(err)
	}
	if win.Spec.WindowUntil == nil {
		t.Fatal("expected cooldown after SpareReady")
	}
	if win.Status.Phase != egv1a1.WindowPhaseCooling {
		t.Fatalf("phase=%s, want Cooling", win.Status.Phase)
	}
}

func putHPA(t *testing.T, c client.Client, min int32) {
	t.Helper()
	hpa := &autoscalingv1.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "app"},
		Spec: autoscalingv1.HorizontalPodAutoscalerSpec{
			MinReplicas: ptr.To(min),
			MaxReplicas: 10,
			ScaleTargetRef: autoscalingv1.CrossVersionObjectReference{
				Kind: "Deployment", Name: "web", APIVersion: "apps/v1",
			},
		},
	}
	if err := c.Create(context.Background(), hpa); err != nil {
		t.Fatal(err)
	}
	hpa.Status = autoscalingv1.HorizontalPodAutoscalerStatus{CurrentReplicas: 3, DesiredReplicas: 3}
	if err := c.Status().Update(context.Background(), hpa); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyScalesDeploymentAndHPA(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	dep.Annotations = map[string]string{egv1a1.ScaleBackendAnnotation: "deployment,hpa"}
	if err := c.Update(ctx, dep); err != nil {
		t.Fatal(err)
	}
	putHPA(t, c, 2)

	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if *dep.Spec.Replicas != 4 {
		t.Fatalf("replicas=%d, want 4", *dep.Spec.Replicas)
	}
	hpa := &autoscalingv1.HorizontalPodAutoscaler{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, hpa); err != nil {
		t.Fatal(err)
	}
	if hpa.Spec.MinReplicas == nil || *hpa.Spec.MinReplicas != 4 {
		t.Fatalf("hpa minReplicas=%v, want 4", hpa.Spec.MinReplicas)
	}
	win := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}, win); err != nil {
		t.Fatal(err)
	}
	if len(win.Spec.Actions) != 2 {
		t.Fatalf("actions=%d %+v", len(win.Spec.Actions), win.Spec.Actions)
	}
	byKey := map[string]egv1a1.ScaleAction{}
	for _, a := range win.Spec.Actions {
		byKey[a.Key] = a
	}
	if byKey["deployment"].Baseline != 3 || byKey["hpa"].Baseline != 2 {
		t.Fatalf("baselines deploy=%d hpa=%d", byKey["deployment"].Baseline, byKey["hpa"].Baseline)
	}
	if byKey["deployment"].Name != "web" || byKey["hpa"].Name != "web" {
		t.Fatalf("catalog keys missing on actions: %+v", win.Spec.Actions)
	}
	// Binding order from the annotation is preserved on the Window.
	if win.Spec.Actions[0].Key != "deployment" || win.Spec.Actions[1].Key != "hpa" {
		t.Fatalf("action order=%+v, want deployment then hpa", win.Spec.Actions)
	}
}

func TestWindowHeldWhenHPAPastSpare(t *testing.T) {
	c, pr, wr := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	dep.Annotations = map[string]string{egv1a1.ScaleBackendAnnotation: "deployment,hpa"}
	if err := c.Update(ctx, dep); err != nil {
		t.Fatal(err)
	}
	putHPA(t, c, 2)

	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}

	node := &corev1.Node{}
	if err := c.Get(ctx, types.NamespacedName{Name: "worker-1"}, node); err != nil {
		t.Fatal(err)
	}
	node.Spec.Taints = nil
	if err := c.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	putSafeReadyPods(t, c, 3)

	hpa := &autoscalingv1.HorizontalPodAutoscaler{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, hpa); err != nil {
		t.Fatal(err)
	}
	hpa.Status.DesiredReplicas = 6
	hpa.Status.CurrentReplicas = 6
	if err := c.Status().Update(ctx, hpa); err != nil {
		t.Fatal(err)
	}

	winName := WindowName("spot-workers", "app", "web")
	if _, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "app", Name: winName}}); err != nil {
		t.Fatal(err)
	}
	win := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: winName}, win); err != nil {
		t.Fatal(err)
	}
	if win.Status.Phase != egv1a1.WindowPhaseHeld {
		t.Fatalf("phase=%s, want Held", win.Status.Phase)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if *dep.Spec.Replicas != 4 {
		t.Fatalf("replicas=%d, want still 4 while Held", *dep.Spec.Replicas)
	}
}

func TestHPAOnlyRestoreClampsToCurrentReplicas(t *testing.T) {
	c, pr, wr := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	dep.Annotations = map[string]string{egv1a1.ScaleBackendAnnotation: "hpa"}
	if err := c.Update(ctx, dep); err != nil {
		t.Fatal(err)
	}
	putHPA(t, c, 2)

	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	hpa := &autoscalingv1.HorizontalPodAutoscaler{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, hpa); err != nil {
		t.Fatal(err)
	}
	if hpa.Spec.MinReplicas == nil || *hpa.Spec.MinReplicas != 3 {
		t.Fatalf("minReplicas=%v, want 3 after scale-up (baseline 2 + spare)", hpa.Spec.MinReplicas)
	}

	node := &corev1.Node{}
	if err := c.Get(ctx, types.NamespacedName{Name: "worker-1"}, node); err != nil {
		t.Fatal(err)
	}
	node.Spec.Taints = nil
	if err := c.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	putSafeReadyPods(t, c, 3)

	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, hpa); err != nil {
		t.Fatal(err)
	}
	// Below ScaledTo so we do not Held; CurrentReplicas above baseline so G4 clamp applies.
	hpa.Status.DesiredReplicas = 3
	hpa.Status.CurrentReplicas = 3
	if err := c.Status().Update(ctx, hpa); err != nil {
		t.Fatal(err)
	}

	winName := WindowName("spot-workers", "app", "web")
	fixed := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	wr.Now = func() time.Time { return fixed }
	if _, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "app", Name: winName}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, hpa); err != nil {
		t.Fatal(err)
	}
	if hpa.Spec.MinReplicas == nil || *hpa.Spec.MinReplicas != 3 {
		t.Fatalf("minReplicas=%v, want 3 (clamped to currentReplicas, not baseline 2)", ptrVal(hpa.Spec.MinReplicas))
	}
}

func ptrVal(p *int32) int32 {
	if p == nil {
		return -1
	}
	return *p
}

func TestWindowScaleBackRestoresDeploymentAndHPA(t *testing.T) {
	c, pr, wr := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	dep.Annotations = map[string]string{egv1a1.ScaleBackendAnnotation: "deployment,hpa"}
	if err := c.Update(ctx, dep); err != nil {
		t.Fatal(err)
	}
	putHPA(t, c, 2)

	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}

	node := &corev1.Node{}
	if err := c.Get(ctx, types.NamespacedName{Name: "worker-1"}, node); err != nil {
		t.Fatal(err)
	}
	node.Spec.Taints = nil
	if err := c.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}

	putSafeReadyPods(t, c, 3)

	winName := WindowName("spot-workers", "app", "web")
	fixed := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	wr.Now = func() time.Time { return fixed }
	if _, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "app", Name: winName}}); err != nil {
		t.Fatal(err)
	}

	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if *dep.Spec.Replicas != 3 {
		t.Fatalf("replicas=%d, want 3 at cooldown start", *dep.Spec.Replicas)
	}
	hpa := &autoscalingv1.HorizontalPodAutoscaler{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, hpa); err != nil {
		t.Fatal(err)
	}
	if hpa.Spec.MinReplicas == nil || *hpa.Spec.MinReplicas != 2 {
		t.Fatalf("hpa minReplicas=%v, want 2 at cooldown start", hpa.Spec.MinReplicas)
	}
}

func TestPolicyHPAOnlyRequiresScaleBackendAnnotation(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	policy := &egv1a1.EvictionGuardPolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: "spot-workers"}, policy); err != nil {
		t.Fatal(err)
	}
	policy.Spec.Backends = map[string]egv1a1.BackendCatalogEntry{
		"hpa": {
			APIVersion: "autoscaling/v1",
			Kind:       "HorizontalPodAutoscaler",
			Patches:    []egv1a1.BackendPatch{{Path: "spec.minReplicas"}},
		},
	}
	if err := c.Update(ctx, policy); err != nil {
		t.Fatal(err)
	}
	putHPA(t, c, 2)

	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}, &egv1a1.EvictionGuardWindow{}); err == nil {
		t.Fatal("window must not open without scale-backend listing hpa")
	}

	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	dep.Annotations = map[string]string{egv1a1.ScaleBackendAnnotation: "hpa"}
	if err := c.Update(ctx, dep); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	hpa := &autoscalingv1.HorizontalPodAutoscaler{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, hpa); err != nil {
		t.Fatal(err)
	}
	if hpa.Spec.MinReplicas == nil || *hpa.Spec.MinReplicas != 3 {
		t.Fatalf("hpa minReplicas=%v, want 3 after scale-backend annotation (baseline 2 + spare 1)", ptr.Deref(hpa.Spec.MinReplicas, -1))
	}
}

func TestPolicyRequiresScaleBackendAnnotation(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	putHPA(t, c, 2)

	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	delete(dep.Annotations, egv1a1.ScaleBackendAnnotation)
	if err := c.Update(ctx, dep); err != nil {
		t.Fatal(err)
	}

	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}, &egv1a1.EvictionGuardWindow{}); err == nil {
		t.Fatal("window must not open without scale-backend")
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 3 {
		t.Fatalf("replicas=%v, want unchanged 3 without scale-backend", dep.Spec.Replicas)
	}
	hpa := &autoscalingv1.HorizontalPodAutoscaler{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, hpa); err != nil {
		t.Fatal(err)
	}
	if hpa.Spec.MinReplicas == nil || *hpa.Spec.MinReplicas != 2 {
		t.Fatalf("hpa minReplicas=%v, want unchanged baseline 2", hpa.Spec.MinReplicas)
	}
}

func TestPolicyHonorsScaleBackAfterAnnotation(t *testing.T) {
	c, pr, wr := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	dep.Annotations = map[string]string{
		egv1a1.ScaleBackendAnnotation:   "deployment",
		egv1a1.ScaleBackAfterAnnotation: "5m",
	}
	if err := c.Update(ctx, dep); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	win := &egv1a1.EvictionGuardWindow{}
	winName := types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}
	if err := c.Get(ctx, winName, win); err != nil {
		t.Fatal(err)
	}
	if win.Spec.WindowUntil != nil {
		t.Fatalf("windowUntil=%v, want unset while nodes are still vulnerable", win.Spec.WindowUntil)
	}

	node := &corev1.Node{}
	if err := c.Get(ctx, types.NamespacedName{Name: "worker-1"}, node); err != nil {
		t.Fatal(err)
	}
	node.Spec.Taints = nil
	if err := c.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, winName, win); err != nil {
		t.Fatal(err)
	}
	if win.Spec.WindowUntil != nil {
		t.Fatalf("windowUntil=%v, want unset until the window controller arms cooldown", win.Spec.WindowUntil)
	}

	putSafeReadyPods(t, c, 3)

	if _, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: winName}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, winName, win); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 8, 30, 12, 5, 0, 0, time.UTC)
	if win.Spec.WindowUntil == nil || !win.Spec.WindowUntil.Time.Equal(want) {
		t.Fatalf("windowUntil=%v, want %s after cooldown starts", win.Spec.WindowUntil, want)
	}

	pr.Now = func() time.Time { return want }
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, winName, win); err != nil {
		t.Fatal(err)
	}
	if win.Spec.WindowUntil == nil || !win.Spec.WindowUntil.Time.Equal(want) {
		t.Fatalf("windowUntil reset to %v, want %s to stay armed", win.Spec.WindowUntil, want)
	}
}

func TestPolicyStampsDeploymentAnnotations(t *testing.T) {
	c, pr, wr := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	dep.Annotations = map[string]string{
		egv1a1.ScaleBackendAnnotation: "deployment",
		egv1a1.StampAnnotation:        "true",
	}
	if err := c.Update(ctx, dep); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if dep.Annotations[egv1a1.StampScaledToKey] != "4" || dep.Annotations[egv1a1.StampBaselineKey] != "3" || dep.Annotations[egv1a1.StampActiveKey] != "true" {
		t.Fatalf("deployment stamps=%v", dep.Annotations)
	}
	if _, ok := dep.Annotations[egv1a1.StampWindowUntilKey]; ok {
		t.Fatalf("window-until stamped while Open: %v", dep.Annotations)
	}

	node := &corev1.Node{}
	if err := c.Get(ctx, types.NamespacedName{Name: "worker-1"}, node); err != nil {
		t.Fatal(err)
	}
	node.Spec.Taints = nil
	if err := c.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if _, ok := dep.Annotations[egv1a1.StampWindowUntilKey]; ok {
		t.Fatalf("window-until stamped before cooldown: %v", dep.Annotations)
	}
	putSafeReadyPods(t, c, 3)
	if _, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if dep.Annotations[egv1a1.StampWindowUntilKey] != "2026-08-30T12:01:00Z" {
		t.Fatalf("window-until after cooldown start=%v", dep.Annotations)
	}
}

func addOptedInWorkload(t *testing.T, c client.Client, name string) {
	t.Helper()
	replicas := int32(3)
	trueVal := true
	uid := types.UID(name + "-uid")
	rsUID := types.UID(name + "-rs-uid")
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "app",
			UID:       uid,
			Annotations: map[string]string{
				egv1a1.ScaleBackendAnnotation: "deployment",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
					"app":                 name,
					egv1a1.ProtectedLabel: "true",
				}},
			},
		},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-rs",
			Namespace: "app",
			UID:       rsUID,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "Deployment", Name: name, UID: uid, Controller: &trueVal,
			}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-pod",
			Namespace: "app",
			Labels: map[string]string{
				"app":                 name,
				egv1a1.ProtectedLabel: "true",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: name + "-rs", UID: rsUID, Controller: &trueVal,
			}},
		},
		Spec: corev1.PodSpec{NodeName: "worker-1"},
	}
	for _, obj := range []client.Object{dep, rs, pod} {
		if err := c.Create(context.Background(), obj); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPolicyContinuesAfterSiblingScaleFailure(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	// Sorted before "web" — would previously abort the loop before web scaled.
	addOptedInWorkload(t, c, "api")
	api := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "api"}, api); err != nil {
		t.Fatal(err)
	}
	api.Annotations = map[string]string{egv1a1.ScaleBackendAnnotation: "deployment,hipaa"}
	if err := c.Update(ctx, api); err != nil {
		t.Fatal(err)
	}

	res, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != requeueDeferred {
		t.Fatalf("requeue=%v, want %v to retry failed sibling", res.RequeueAfter, requeueDeferred)
	}

	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "api")}, &egv1a1.EvictionGuardWindow{}); err == nil {
		t.Fatal("api must not get a window with invalid scale-backend")
	}
	webWin := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}, webWin); err != nil {
		t.Fatalf("web should still scale despite api failure: %v", err)
	}
	web := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, web); err != nil {
		t.Fatal(err)
	}
	if web.Spec.Replicas == nil || *web.Spec.Replicas != 4 {
		t.Fatalf("web replicas=%v, want 4", web.Spec.Replicas)
	}
	p := &egv1a1.EvictionGuardPolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: "spot-workers"}, p); err != nil {
		t.Fatal(err)
	}
	if len(p.Status.Conditions) == 0 || p.Status.Conditions[0].Status != metav1.ConditionFalse {
		t.Fatalf("policy Ready=%v, want False summarizing api failure", p.Status.Conditions)
	}
}

func setWindowCap(t *testing.T, c client.Client, n int32) {
	t.Helper()
	p := &egv1a1.EvictionGuardPolicy{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "spot-workers"}, p); err != nil {
		t.Fatal(err)
	}
	p.Spec.MaxConcurrentWindows = ptr.To(n)
	if err := c.Update(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyCapsNewWindows(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	addOptedInWorkload(t, c, "api")
	setWindowCap(t, c, 1)

	res, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != requeueDeferred {
		t.Fatalf("requeue=%v, want %v while a workload is deferred", res.RequeueAfter, requeueDeferred)
	}

	api := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "api"}, api); err != nil {
		t.Fatal(err)
	}
	web := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, web); err != nil {
		t.Fatal(err)
	}
	if *api.Spec.Replicas != 4 {
		t.Fatalf("api replicas=%d, want 4 (first admitted)", *api.Spec.Replicas)
	}
	if *web.Spec.Replicas != 3 {
		t.Fatalf("web replicas=%d, want 3 (deferred)", *web.Spec.Replicas)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}, &egv1a1.EvictionGuardWindow{}); err == nil {
		t.Fatal("web should not have a window")
	}
	p := &egv1a1.EvictionGuardPolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: "spot-workers"}, p); err != nil {
		t.Fatal(err)
	}
	if p.Status.ActiveWindows != 1 || p.Status.DeferredWorkloads != 1 {
		t.Fatalf("active=%d deferred=%d", p.Status.ActiveWindows, p.Status.DeferredWorkloads)
	}
}

func TestPolicyUpdatesExistingWindowAtCap(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	addOptedInWorkload(t, c, "api")
	setWindowCap(t, c, 1)
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}

	trueVal := true
	if err := c.Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-pod-2",
			Namespace: "app",
			Labels: map[string]string{
				"app":                 "api",
				egv1a1.ProtectedLabel: "true",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "api-rs", UID: "api-rs-uid", Controller: &trueVal,
			}},
		},
		Spec: corev1.PodSpec{NodeName: "worker-1"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	api := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "api"}, api); err != nil {
		t.Fatal(err)
	}
	if *api.Spec.Replicas != 5 {
		t.Fatalf("api replicas=%d, want 5 (existing window still updates)", *api.Spec.Replicas)
	}
	web := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, web); err != nil {
		t.Fatal(err)
	}
	if *web.Spec.Replicas != 3 {
		t.Fatalf("web replicas=%d, want 3 still deferred", *web.Spec.Replicas)
	}
}

func TestPolicyOpensNextWindowAfterSlotFrees(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	addOptedInWorkload(t, c, "api")
	setWindowCap(t, c, 1)
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}

	// api is no longer at risk and its window is gone (Closed + deleted).
	if err := c.Delete(ctx, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-pod", Namespace: "app"}}); err != nil {
		t.Fatal(err)
	}
	win := &egv1a1.EvictionGuardWindow{}
	winNN := types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "api")}
	if err := c.Get(ctx, winNN, win); err != nil {
		t.Fatal(err)
	}
	win.Finalizers = nil
	if err := c.Update(ctx, win); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, win); err != nil {
		t.Fatal(err)
	}

	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	web := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, web); err != nil {
		t.Fatal(err)
	}
	if *web.Spec.Replicas != 4 {
		t.Fatalf("web replicas=%d, want 4 after a slot freed", *web.Spec.Replicas)
	}
}

func TestPolicyUnlimitedWindowsWhenCapZero(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	addOptedInWorkload(t, c, "api")
	setWindowCap(t, c, 0)
	res, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("requeue=%v, want none when unlimited", res.RequeueAfter)
	}
	for _, name := range []string{"api", "web"} {
		dep := &appsv1.Deployment{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: name}, dep); err != nil {
			t.Fatal(err)
		}
		if *dep.Spec.Replicas != 4 {
			t.Fatalf("%s replicas=%d, want 4", name, *dep.Spec.Replicas)
		}
	}
	p := &egv1a1.EvictionGuardPolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: "spot-workers"}, p); err != nil {
		t.Fatal(err)
	}
	if p.Status.ActiveWindows != 2 || p.Status.DeferredWorkloads != 0 {
		t.Fatalf("active=%d deferred=%d", p.Status.ActiveWindows, p.Status.DeferredWorkloads)
	}
}

func TestMaxWindowForceCoolsStuckOpen(t *testing.T) {
	c, pr, wr := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	p := &egv1a1.EvictionGuardPolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: "spot-workers"}, p); err != nil {
		t.Fatal(err)
	}
	p.Spec.MaxWindow = &metav1.Duration{Duration: 30 * time.Minute}
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}

	winNN := types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}
	opened := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	wr.Now = func() time.Time { return opened.Add(30 * time.Minute) }
	if _, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: winNN}); err != nil {
		t.Fatal(err)
	}
	win := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(ctx, winNN, win); err != nil {
		t.Fatal(err)
	}
	if win.Status.Phase != egv1a1.WindowPhaseCooling || !win.Status.ForcedCool {
		t.Fatalf("phase=%s forcedCool=%v, want Cooling+ForcedCool", win.Status.Phase, win.Status.ForcedCool)
	}
	wantUntil := opened.Add(31 * time.Minute)
	if win.Spec.WindowUntil == nil || !win.Spec.WindowUntil.Time.Equal(wantUntil) {
		t.Fatalf("windowUntil=%v, want %s", win.Spec.WindowUntil, wantUntil)
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if *dep.Spec.Replicas != 3 {
		t.Fatalf("replicas=%d, want 3 at maxWindow cooldown start", *dep.Spec.Replicas)
	}

	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, winNN, win); err != nil {
		t.Fatal(err)
	}
	if win.Spec.WindowUntil == nil || win.Status.Phase != egv1a1.WindowPhaseCooling {
		t.Fatalf("policy reset force-cool: until=%v phase=%s", win.Spec.WindowUntil, win.Status.Phase)
	}

	wr.Now = func() time.Time { return opened.Add(50 * time.Minute) }
	if _, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: winNN}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if *dep.Spec.Replicas != 3 {
		t.Fatalf("replicas=%d, want 3 after maxWindow scale-back", *dep.Spec.Replicas)
	}
	if err := c.Get(ctx, winNN, win); err != nil {
		t.Fatal(err)
	}
	if win.Status.Phase != egv1a1.WindowPhaseClosed || !win.Status.ForcedCool {
		t.Fatalf("tombstone phase=%s forcedCool=%v", win.Status.Phase, win.Status.ForcedCool)
	}

	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if *dep.Spec.Replicas != 3 {
		t.Fatalf("replicas=%d, want 3 (tombstone blocks reopen)", *dep.Spec.Replicas)
	}

	node := &corev1.Node{}
	if err := c.Get(ctx, types.NamespacedName{Name: "worker-1"}, node); err != nil {
		t.Fatal(err)
	}
	node.Spec.Taints = nil
	if err := c.Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: winNN}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, winNN, win); err == nil {
		t.Fatal("expected tombstone deleted after nodes cleared")
	}
}

func TestMaxWindowZeroIsUnlimited(t *testing.T) {
	c, pr, wr := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	p := &egv1a1.EvictionGuardPolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: "spot-workers"}, p); err != nil {
		t.Fatal(err)
	}
	p.Spec.MaxWindow = &metav1.Duration{Duration: 0}
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	wr.Now = func() time.Time { return time.Date(2026, 8, 30, 18, 0, 0, 0, time.UTC) }
	winNN := types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}
	if _, err := wr.Reconcile(ctx, ctrl.Request{NamespacedName: winNN}); err != nil {
		t.Fatal(err)
	}
	win := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(ctx, winNN, win); err != nil {
		t.Fatal(err)
	}
	if win.Status.Phase != egv1a1.WindowPhaseOpen || win.Status.ForcedCool {
		t.Fatalf("phase=%s forcedCool=%v, want Open (unlimited)", win.Status.Phase, win.Status.ForcedCool)
	}
}

func TestFirstMatchingPolicyByNameOwnsWorkload(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	// Lexicographically before spot-workers — should win when both match.
	other := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha-pool", UID: "alpha-uid", Finalizers: []string{egv1a1.PolicyFinalizer}},
		Spec: egv1a1.EvictionGuardPolicySpec{
			NodeFilter:    egv1a1.NodeFilter{CapacityTypes: []string{"spot"}},
			SpareReplicas: ptr.To(int32(1)),
			MaxBuffer:     ptr.To(int32(4)),
			Backends:      defaultCatalogBackends(),
		},
	}
	if err := c.Create(ctx, other); err != nil {
		t.Fatal(err)
	}

	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}, &egv1a1.EvictionGuardWindow{}); err == nil {
		t.Fatal("spot-workers must not open a window when alpha-pool wins by name")
	}

	prOther := &PolicyReconciler{Client: c, Scheme: pr.Scheme, Recorder: events.NewFakeRecorder(8), Now: pr.Now}
	if _, err := prOther.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "alpha-pool"}}); err != nil {
		t.Fatal(err)
	}
	win := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: WindowName("alpha-pool", "app", "web")}, win); err != nil {
		t.Fatal(err)
	}
	if win.Spec.PolicyName != "alpha-pool" {
		t.Fatalf("policyName=%s", win.Spec.PolicyName)
	}
}

func TestPolicyPinOverridesNameOrder(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	alpha := &egv1a1.EvictionGuardPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "alpha-pool", UID: "alpha-uid", Finalizers: []string{egv1a1.PolicyFinalizer}},
		Spec: egv1a1.EvictionGuardPolicySpec{
			NodeFilter:    egv1a1.NodeFilter{CapacityTypes: []string{"spot"}},
			SpareReplicas: ptr.To(int32(1)),
			MaxBuffer:     ptr.To(int32(4)),
			Backends:      defaultCatalogBackends(),
		},
	}
	if err := c.Create(ctx, alpha); err != nil {
		t.Fatal(err)
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	dep.Annotations = map[string]string{
		egv1a1.ScaleBackendAnnotation: "deployment",
		egv1a1.PolicyPinAnnotation:    "spot-workers",
	}
	if err := c.Update(ctx, dep); err != nil {
		t.Fatal(err)
	}

	prAlpha := &PolicyReconciler{Client: c, Scheme: pr.Scheme, Recorder: events.NewFakeRecorder(8), Now: pr.Now}
	if _, err := prAlpha.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "alpha-pool"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: WindowName("alpha-pool", "app", "web")}, &egv1a1.EvictionGuardWindow{}); err == nil {
		t.Fatal("alpha-pool must not own a pinned workload")
	}

	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	win := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}, win); err != nil {
		t.Fatal(err)
	}
	if win.Spec.PolicyName != "spot-workers" {
		t.Fatalf("policyName=%s", win.Spec.PolicyName)
	}
}

func TestPolicyCatalogBackendsScaleDeployment(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	p := &egv1a1.EvictionGuardPolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: "spot-workers"}, p); err != nil {
		t.Fatal(err)
	}
	p.Spec.Backends = map[string]egv1a1.BackendCatalogEntry{
		"deployment": {
			APIVersion: "apps/v1",
			Kind:       "Deployment",
			Patches:    []egv1a1.BackendPatch{{Path: "spec.replicas"}},
		},
		"hpa": {
			APIVersion: "autoscaling/v1",
			Kind:       "HorizontalPodAutoscaler",
			Patches:    []egv1a1.BackendPatch{{Path: "spec.minReplicas"}},
		},
	}
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	dep.Annotations = map[string]string{egv1a1.ScaleBackendAnnotation: "deployment"}
	if err := c.Update(ctx, dep); err != nil {
		t.Fatal(err)
	}
	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 4 {
		t.Fatalf("replicas=%v, want 4", dep.Spec.Replicas)
	}
	win := &egv1a1.EvictionGuardWindow{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}, win); err != nil {
		t.Fatal(err)
	}
	if len(win.Spec.Actions) != 1 || win.Spec.Actions[0].FieldPath != "spec.replicas" {
		t.Fatalf("actions=%+v", win.Spec.Actions)
	}
}

func TestPolicyPinBypassesWorkloadSelector(t *testing.T) {
	c, pr, _ := fixture(t, map[string]string{"karpenter.sh/capacity-type": "spot"}, []corev1.Taint{
		{Key: signals.TaintKarpenterDisrupted, Effect: corev1.TaintEffectNoSchedule},
	})
	ctx := context.Background()
	p := &egv1a1.EvictionGuardPolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: "spot-workers"}, p); err != nil {
		t.Fatal(err)
	}
	p.Spec.WorkloadSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"profile": "web"}}
	if err := c.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: "web"}, dep); err != nil {
		t.Fatal(err)
	}
	// Deployment labels do not match selector, but pin forces ownership.
	dep.Annotations = map[string]string{
		egv1a1.ScaleBackendAnnotation: "deployment",
		egv1a1.PolicyPinAnnotation:    "spot-workers",
	}
	if err := c.Update(ctx, dep); err != nil {
		t.Fatal(err)
	}

	if _, err := pr.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "spot-workers"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "app", Name: WindowName("spot-workers", "app", "web")}, &egv1a1.EvictionGuardWindow{}); err != nil {
		t.Fatal(err)
	}
}
