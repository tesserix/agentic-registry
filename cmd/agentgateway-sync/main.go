package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tesserix/agentic-registry/internal/gatewaysync"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{AddSource: true}))
	config, err := gatewaysync.LoadConfig()
	if err != nil {
		logger.Error("load configuration", "err", err)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, config, logger); err != nil {
		logger.Error("agentgateway sync stopped", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, config gatewaysync.Config, logger *slog.Logger) error {
	ctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("load in-cluster Kubernetes configuration: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("create dynamic Kubernetes client: %w", err)
	}
	typedClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("create typed Kubernetes client: %w", err)
	}
	registryClient, err := gatewaysync.NewRegistryClient(gatewaysync.RegistryClientOptions{
		URL:             config.RegistryURL,
		TokenFile:       config.RegistryTokenFile,
		TargetNamespace: config.TargetNamespace,
		MinResources:    config.MinResources,
		MaxBodyBytes:    config.MaxBodyBytes,
		HTTPClient: &http.Client{
			Timeout: config.RegistryRequestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	})
	if err != nil {
		return err
	}
	kubernetesClient, err := gatewaysync.NewDynamicKubernetes(dynamicClient, config.FieldManager)
	if err != nil {
		return err
	}
	reconciler, err := gatewaysync.NewReconciler(kubernetesClient, gatewaysync.ReconcilerOptions{
		TargetNamespace:        config.TargetNamespace,
		Mode:                   config.Mode,
		Prune:                  config.Prune,
		AcceptanceTimeout:      config.AcceptanceTimeout,
		AcceptancePollInterval: config.AcceptancePollInterval,
	})
	if err != nil {
		return err
	}
	status := gatewaysync.NewStatus(config.Mode)
	controller, err := gatewaysync.NewController(registryClient, reconciler, status, gatewaysync.ControllerOptions{
		PollInterval:   config.PollInterval,
		SafetyInterval: config.SafetyInterval,
	})
	if err != nil {
		return err
	}
	watcher, err := gatewaysync.NewDriftWatcher(dynamicClient, config.TargetNamespace, config.WatchResyncInterval, controller.TriggerDrift)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              config.HTTPAddress,
		Handler:           gatewaysync.NewStatusHTTPHandler(status),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	runtimeErrors := make(chan error, 2)
	go func() {
		logger.Info("status server listening", "address", config.HTTPAddress)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			runtimeErrors <- fmt.Errorf("serve status HTTP: %w", err)
		}
	}()

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{Name: config.LeaseName, Namespace: config.PodNamespace},
		Client:    typedClient.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: config.PodName,
		},
	}
	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   config.LeaseDuration,
		RenewDeadline:   config.LeaseRenewDeadline,
		RetryPeriod:     config.LeaseRetryPeriod,
		ReleaseOnCancel: true,
		Name:            "agentgateway-route-sync",
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(leaderContext context.Context) {
				status.SetLeader(true)
				logger.Info("reconciliation leadership acquired", "identity", config.PodName, "mode", config.Mode)
				group, groupContext := errgroup.WithContext(leaderContext)
				group.Go(func() error { return controller.Run(groupContext) })
				group.Go(func() error { return watcher.Run(groupContext) })
				if err := group.Wait(); err != nil {
					select {
					case runtimeErrors <- fmt.Errorf("run leader controllers: %w", err):
					default:
					}
				}
			},
			OnStoppedLeading: func() {
				status.SetLeader(false)
				logger.Warn("reconciliation leadership released", "identity", config.PodName)
			},
			OnNewLeader: func(identity string) {
				if identity != config.PodName {
					logger.Info("reconciliation leader observed", "identity", identity)
				}
			},
		},
	})
	if err != nil {
		return fmt.Errorf("construct leader elector: %w", err)
	}
	electionDone := make(chan struct{})
	status.SetReady(true)
	go func() {
		defer close(electionDone)
		elector.Run(ctx)
	}()

	var runtimeErr error
	select {
	case <-ctx.Done():
	case runtimeErr = <-runtimeErrors:
	case <-electionDone:
		if ctx.Err() == nil {
			runtimeErr = errors.New("leader election stopped unexpectedly")
		}
	}
	cancelRun()
	status.SetReady(false)
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownContext)
	if shutdownErr != nil {
		shutdownErr = fmt.Errorf("shutdown status HTTP: %w", shutdownErr)
	}
	return errors.Join(runtimeErr, shutdownErr)
}
