//go:build envtest

/*
Copyright 2026 Whitemug.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package envtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/internal/controller"
)

var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	stopMgr   context.CancelFunc
)

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	if err := startEnv(); err != nil {
		fmt.Fprintf(os.Stderr, "envtest setup: %v\nrun via: make test-envtest\n", err)
		os.Exit(1)
	}
	code := m.Run()
	stopEnv()
	os.Exit(code)
}

func startEnv() error {
	_, file, _, _ := goruntime.Caller(0)
	crdDir := filepath.Join(filepath.Dir(file), "..", "..", "..", "config", "crd", "bases")
	testdataCRDDir := filepath.Join(filepath.Dir(file), "testdata", "crds")

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := egv1a1.AddToScheme(scheme); err != nil {
		return err
	}
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		return err
	}

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdDir, testdataCRDDir},
		ErrorIfCRDPathMissing: true,
		Scheme:                scheme,
	}
	cfg, err := testEnv.Start()
	if err != nil {
		return err
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	if err != nil {
		return err
	}

	if err := (&controller.PolicyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("envtest-policy"),
	}).SetupWithManager(mgr); err != nil {
		return err
	}
	if err := (&controller.WindowReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("envtest-window"),
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	stopMgr = cancel
	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "manager: %v\n", err)
		}
	}()

	direct, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		cancel()
		return err
	}
	k8sClient = direct

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		list := &egv1a1.EvictionGuardPolicyList{}
		if err := mgr.GetClient().List(ctx, list); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	return fmt.Errorf("manager cache did not become ready")
}

func stopEnv() {
	if stopMgr != nil {
		stopMgr()
	}
	if testEnv != nil {
		_ = testEnv.Stop()
	}
}

func eventually(t *testing.T, timeout time.Duration, fn func(ctx context.Context) error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		last = fn(ctx)
		cancel()
		if last == nil {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("timeout after %s: %v", timeout, last)
}

func consistently(t *testing.T, duration time.Duration, fn func(ctx context.Context) error) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := fn(ctx)
		cancel()
		if err != nil {
			t.Fatalf("broke consistency within %s: %v", duration, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func createNS(t *testing.T, name string) {
	t.Helper()
	ns := &corev1.Namespace{}
	ns.Name = name
	if err := k8sClient.Create(context.Background(), ns); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), ns)
	})
}
