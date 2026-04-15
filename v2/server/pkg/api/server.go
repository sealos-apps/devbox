package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	devboxv1alpha2 "github.com/sealos-apps/devbox/v2/controller/api/v1alpha2"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type apiServer struct {
	cfg        ServerConfig
	ctrlClient ctrlclient.Client
	kubeClient kubernetes.Interface
	logger     *slog.Logger

	lifecycleNotifyCh chan lifecycleSignal
	lifecycleReader   ctrlclient.Reader

	jwtSecretCacheMu sync.RWMutex
	jwtSecretCache   map[string]jwtSecretCacheEntry

	gatewayIndexMu         sync.RWMutex
	gatewayIndexByUniqueID map[string]gatewayIndexEntry
	gatewayIndexByDevbox   map[string]string

	gatewayProxyTransport http.RoundTripper
}

type jwtSecretCacheEntry struct {
	token     string
	expiresAt time.Time
}

func Run(configPath string) error {
	cfg, err := loadServerConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config failed: %w", err)
	}

	restCfg := ctrl.GetConfigOrDie()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add core scheme failed: %w", err)
	}
	if err := devboxv1alpha2.AddToScheme(scheme); err != nil {
		return fmt.Errorf("add devbox scheme failed: %w", err)
	}

	ctrlClient, err := ctrlclient.New(restCfg, ctrlclient.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("create controller-runtime client failed: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("create kube clientset failed: %w", err)
	}

	logger := newLogger(os.Stdout, cfg.LogLevel)

	app := &apiServer{
		cfg:        cfg,
		ctrlClient: ctrlClient,
		kubeClient: kubeClient,
		logger:     logger,

		lifecycleNotifyCh:      make(chan lifecycleSignal, 64),
		jwtSecretCache:         make(map[string]jwtSecretCacheEntry, 128),
		gatewayIndexByUniqueID: make(map[string]gatewayIndexEntry, 128),
		gatewayIndexByDevbox:   make(map[string]string, 128),
		gatewayProxyTransport:  newGatewayProxyTransport(),
	}
	app.logInfo(
		"devbox api server starting",
		"api_addr", cfg.Addr,
		"gateway_addr", cfg.GatewayAddr,
		"config_path", configPath,
		"log_level", cfg.LogLevel.String(),
		"auth_mode", "jwt-hs256",
		"ssh_host", cfg.SSH.Host,
		"ssh_port", cfg.SSH.Port,
		"ssh_user", cfg.SSH.User,
		"create_cpu", cfg.CreateResource.CPU,
		"create_memory", cfg.CreateResource.Memory,
		"create_storage_limit", cfg.CreateResource.StorageLimit,
		"create_image", cfg.CreateResource.Image,
	)

	apiHTTPServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.apiRoutes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	gatewayHTTPServer := &http.Server{
		Addr:              cfg.GatewayAddr,
		Handler:           app.gatewayRoutes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	stopCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-stopCtx.Done()
		app.logInfo("shutdown signal received")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = apiHTTPServer.Shutdown(shutdownCtx)
		_ = gatewayHTTPServer.Shutdown(shutdownCtx)
	}()

	if err := app.startLifecycleCache(stopCtx, restCfg, scheme); err != nil {
		return fmt.Errorf("start lifecycle cache failed: %w", err)
	}
	app.startLifecycleRunner(stopCtx)

	serveErrCh := make(chan error, 2)
	go app.serveHTTP(stopCtx, "api", apiHTTPServer, serveErrCh)
	go app.serveHTTP(stopCtx, "gateway", gatewayHTTPServer, serveErrCh)

	var firstErr error
	for i := 0; i < 2; i++ {
		err := <-serveErrCh
		if err != nil && firstErr == nil {
			firstErr = err
			stop()
		}
	}
	if firstErr != nil {
		return firstErr
	}
	app.logInfo("devbox api server stopped")
	return nil
}

func (s *apiServer) serveHTTP(ctx context.Context, name string, server *http.Server, errCh chan<- error) {
	if server == nil {
		errCh <- nil
		return
	}

	s.logInfo("http server listening", "server", name, "addr", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		errCh <- fmt.Errorf("%s http server exited unexpectedly: %w", name, err)
		return
	}
	if ctx.Err() == nil {
		s.logInfo("http server stopped", "server", name, "addr", server.Addr)
	}
	errCh <- nil
}
