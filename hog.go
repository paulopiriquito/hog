// Package hog is the HOG v2 application gateway framework: a standard-library-first
// Go gateway that serves static frontends and acts as their BFF.
package hog

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/paulopiriquito/hog/app"
	"github.com/paulopiriquito/hog/idp"
	"github.com/paulopiriquito/hog/internal/configschema"
	"github.com/paulopiriquito/hog/registry"
	"github.com/paulopiriquito/hog/terminal"
)

// Register registers a module on the default registry. Call it from a plugin's
// init(). Re-exported so plugin authors import only this package.
func Register(kind, name string, f registry.Factory) { registry.Register(kind, name, f) }

// Run loads config from path and serves until the context is cancelled.
func Run(ctx context.Context, path string) error {
	terminal.Register(registry.Default) // built-in terminal handlers
	idp.Register(registry.Default)      // built-in IdP connector
	return app.Run(ctx, path, registry.Default, slog.Default())
}

// Main is the standard entrypoint for a HOG binary: parse --config, install a
// signal-cancelled context, and Run. Exits non-zero on error.
func Main() {
	if len(os.Args) > 1 && os.Args[1] == "schema" {
		if _, err := os.Stdout.Write(configschema.JSON()); err != nil {
			slog.Error("write schema", "err", err)
			os.Exit(1)
		}
		return
	}

	cfgPath := flag.String("config", "/etc/hog", "path to config file or directory")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := Run(ctx, *cfgPath); err != nil {
		slog.Error("hog exited with error", "err", err)
		os.Exit(1)
	}
}
