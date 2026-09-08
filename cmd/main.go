/*
Copyright 2026 Vikas Verma.

Licensed under the MIT License.
See LICENSE in the project root for license information.
*/

package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	egv1a1 "github.com/whitemug/eviction-guard/api/v1alpha1"
	"github.com/whitemug/eviction-guard/internal/controller"
	egwebhook "github.com/whitemug/eviction-guard/internal/webhook"
	_ "github.com/whitemug/eviction-guard/pkg/metrics"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(egv1a1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		webhookCertDir       string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Prometheus metrics bind address")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe bind address")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true, "Enable leader election for HA")
	flag.StringVar(&webhookCertDir, "webhook-cert-dir", "", "Directory with tls.crt/tls.key. Empty disables the validating webhook.")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgrOpts := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "eviction-guard.eviction-guard.io",
	}
	if webhookCertDir != "" {
		mgrOpts.WebhookServer = ctrlwebhook.NewServer(ctrlwebhook.Options{
			Port:    9443,
			CertDir: webhookCertDir,
		})
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := (&controller.PolicyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("eviction-guard-policy"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "EvictionGuardPolicy")
		os.Exit(1)
	}

	if err := (&controller.WindowReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("eviction-guard-window"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "EvictionGuardWindow")
		os.Exit(1)
	}

	if webhookCertDir != "" {
		if err := egwebhook.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook")
			os.Exit(1)
		}
		setupLog.Info("validating webhook enabled", "certDir", webhookCertDir)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting eviction-guard manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
