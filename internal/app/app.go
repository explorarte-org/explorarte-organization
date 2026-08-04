package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/platform/buildinfo"
	"github.com/Mireuz13/explorarte-organization/internal/platform/httpserver"
)

var ErrHTTPServerStopped = errors.New("HTTP server stopped unexpectedly")

type App struct {
	cfg    config.Config
	logger *slog.Logger
	ready  atomic.Bool
	server *httpserver.Server
}

func New(cfg config.Config, logger *slog.Logger, info buildinfo.Info) *App {
	if logger == nil {
		logger = slog.Default()
	}

	application := &App{
		cfg:    cfg,
		logger: logger,
	}
	application.server = httpserver.New(cfg.HTTP, logger, info, application.Ready)

	return application
}

func (a *App) Run(ctx context.Context) error {
	errorsCh, err := a.server.Start()
	if err != nil {
		return fmt.Errorf("start HTTP server: %w", err)
	}

	a.ready.Store(true)
	a.logger.Info("organization kernel is ready", "http_addr", a.server.Addr())

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errorsCh:
		if err != nil {
			runErr = fmt.Errorf("serve HTTP: %w", err)
		} else {
			runErr = ErrHTTPServerStopped
		}
	}

	a.ready.Store(false)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.App.ShutdownTimeout)
	defer cancel()

	if err := a.server.Shutdown(shutdownCtx); err != nil {
		if runErr != nil {
			return errors.Join(runErr, err)
		}
		return err
	}

	if runErr != nil {
		return runErr
	}

	return nil
}

func (a *App) Ready() bool {
	return a.ready.Load()
}

func (a *App) Addr() string {
	return a.server.Addr()
}
