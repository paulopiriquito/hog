package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
)

// Run loads config from path, builds the handler using reg, and serves until
// ctx is cancelled, then shuts down gracefully. It also starts a minimal
// metrics listener on the Gateway's OTEL port if configured.
func Run(ctx context.Context, path string, reg *registry.Registry, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	resources, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg, err := Parse(resources)
	if err != nil {
		return err
	}
	a, err := Build(cfg, reg, logger)
	if err != nil {
		return err
	}

	srv := &http.Server{Addr: cfg.Gateway.Listen, Handler: a.Handler}
	var otel *http.Server
	if cfg.Gateway.OTELPort != "" {
		otel = &http.Server{Addr: cfg.Gateway.OTELPort, Handler: metricsStub()}
	}

	errCh := make(chan error, 2)
	go func() { errCh <- serve(srv) }()
	if otel != nil {
		go func() { errCh <- serve(otel) }()
	}
	logger.Info("hog listening", "addr", cfg.Gateway.Listen, "otel", cfg.Gateway.OTELPort)

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		if otel != nil {
			_ = otel.Shutdown(shutCtx)
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func serve(s *http.Server) error {
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// metricsStub is a placeholder OTEL-port handler; the observability spec
// replaces it with a real metrics exporter.
func metricsStub() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "# hog metrics endpoint (stub)\n")
	})
}
