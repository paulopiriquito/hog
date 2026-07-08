package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/paulopiriquito/hog/config"
	"github.com/paulopiriquito/hog/registry"
	"github.com/paulopiriquito/hog/telemetry"
)

// Run loads config from path, sets up telemetry, builds the handler using reg,
// and serves until ctx is cancelled, then shuts down gracefully (flushing spans
// and metrics).
func Run(ctx context.Context, path string, reg *registry.Registry, logger *slog.Logger) error {
	resources, err := config.Load(path)
	if err != nil {
		return err
	}
	cfg, err := Parse(resources)
	if err != nil {
		return err
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.Telemetry.SlogLevel()}))
	}

	shutdownTel, err := telemetry.Setup(ctx, cfg.Telemetry, logger)
	if err != nil {
		return err
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTel(sctx)
	}()

	a, err := Build(cfg, reg, logger)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: cfg.Gateway.Listen, Handler: a.Handler}

	errCh := make(chan error, 1)
	go func() { errCh <- serve(srv) }()
	logger.Info("hog listening", "addr", cfg.Gateway.Listen)

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
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
