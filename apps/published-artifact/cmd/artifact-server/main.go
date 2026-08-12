package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eli0shin/proxmox-config/apps/published-artifact/internal/httpapi"
	"github.com/eli0shin/proxmox-config/apps/published-artifact/internal/store"
	"github.com/eli0shin/proxmox-config/apps/published-artifact/internal/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	fallbackLogger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	logger, providers, telemetryErr := telemetry.Setup(ctx, getenv("OTEL_EXPORTER_OTLP_ENDPOINT", "datadog-agent.datadog.svc.cluster.local:4317"))
	if telemetryErr != nil {
		logger = fallbackLogger
		logger.Error("OpenTelemetry initialization failed", "error", telemetryErr)
	}
	slog.SetDefault(logger)

	runErr := run(ctx)
	if runErr != nil {
		logger.Error("artifact server stopped with an error", "error", runErr)
	}
	if providers != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		shutdownErr := providers.Shutdown(shutdownCtx)
		cancel()
		if shutdownErr != nil {
			fallbackLogger.Error("OpenTelemetry shutdown failed", "error", shutdownErr)
		}
	}
	stop()
	if runErr != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	catalog, err := store.Open(ctx,
		getenv("ARTIFACT_DATABASE_PATH", "/var/lib/artifact/database/artifacts.db"),
		getenv("ARTIFACT_VERSIONS_PATH", "/var/lib/artifact/versions"),
	)
	if err != nil {
		return err
	}
	defer catalog.Close()

	application := httpapi.New(catalog, getenv("ARTIFACT_PUBLIC_BASE_URL", "https://artifacts.home.arpa"))
	server, err := newHTTPServer(otelhttp.NewHandler(application, "artifact-server.request"))
	if err != nil {
		return err
	}

	serveError := make(chan error, 1)
	go func() {
		slog.Info("artifact server started", "address", server.Addr)
		serveError <- server.ListenAndServe()
	}()

	select {
	case err := <-serveError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		application.Drain()
		slog.Info("artifact server draining")
		if err := server.Shutdown(context.Background()); err != nil {
			return err
		}
		slog.Info("artifact server stopped")
		return nil
	}
}

func newHTTPServer(handler http.Handler) (*http.Server, error) {
	uploadTimeout, err := time.ParseDuration(getenv("ARTIFACT_UPLOAD_TIMEOUT", "1h"))
	if err != nil || uploadTimeout <= 0 {
		return nil, errors.New("ARTIFACT_UPLOAD_TIMEOUT must be a positive duration")
	}
	return &http.Server{
		Addr:              getenv("ARTIFACT_LISTEN_ADDR", ":8080"),
		Handler:           withPublicationTimeout(handler, uploadTimeout),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       uploadTimeout,
		IdleTimeout:       2 * time.Minute,
	}, nil
}

func withPublicationTimeout(next http.Handler, timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/artifacts" {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
