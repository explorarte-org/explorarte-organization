package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Mireuz13/explorarte-organization/internal/app"
	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/platform/buildinfo"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
	"github.com/Mireuz13/explorarte-organization/internal/platform/logging"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	// R9.1 fix 4: see cmd/orgctl/main.go -- same BuildSHA wiring, in case
	// any dispatch path ever runs under this binary too.
	modelruntime.BuildSHA = commit
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load configuration: %v\n", err)
		return 2
	}
	logger := logging.New(os.Stdout, cfg.Logging.Level, cfg.Logging.Format)
	slog.SetDefault(logger)
	info := buildinfo.Info{Version: version, Commit: commit, BuildTime: buildTime}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	application, err := app.New(ctx, cfg, logger, info)
	if err != nil {
		logger.Error("initialize organization kernel", "error", err)
		return 1
	}
	logger.Info("starting organization kernel", "app", cfg.App.Name, "environment", cfg.App.Environment, "version", info.Version, "commit", info.Commit)
	if err := application.Run(ctx); err != nil {
		logger.Error("organization kernel stopped with error", "error", err)
		return 1
	}
	logger.Info("organization kernel stopped")
	return 0
}
