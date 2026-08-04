package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/platform/buildinfo"
	platformmigrations "github.com/Mireuz13/explorarte-organization/internal/platform/migrations"
)

type fakeDatabase struct {
	available atomic.Bool
	closed    atomic.Bool
}

func (database *fakeDatabase) Ping(context.Context) error {
	if !database.available.Load() {
		return errors.New("database unavailable")
	}
	return nil
}
func (database *fakeDatabase) Close() { database.closed.Store(true) }

type fakeMigrator struct{ err error }

func (m fakeMigrator) Up(context.Context) (platformmigrations.Result, error) {
	return platformmigrations.Result{Current: 1}, m.err
}
func (m fakeMigrator) Status(context.Context) (platformmigrations.Status, error) {
	return platformmigrations.Status{Applied: 1, Current: 1, Latest: 1, Ready: m.err == nil}, m.err
}

func TestProcessStaysLiveWhilePostgresIsUnavailable(t *testing.T) {
	cfg := testConfig(t)
	cfg.Database.MigrationRetry = 20 * time.Millisecond
	database := &fakeDatabase{}
	application := newWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), buildinfo.Info{Version: "test"}, database, fakeMigrator{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	waitForAddress(t, application)
	assertHTTPStatus(t, "http://"+application.Addr()+"/healthz", http.StatusOK)
	assertHTTPStatus(t, "http://"+application.Addr()+"/readyz", http.StatusServiceUnavailable)
	database.available.Store(true)
	waitForStatus(t, "http://"+application.Addr()+"/readyz", http.StatusOK)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("application did not stop")
	}
	if !database.closed.Load() {
		t.Fatal("database was not closed")
	}
}

func TestMigrationFailureDoesNotKillHTTPProcess(t *testing.T) {
	cfg := testConfig(t)
	cfg.Database.MigrationRetry = time.Second
	database := &fakeDatabase{}
	database.available.Store(true)
	application := newWithDependencies(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), buildinfo.Info{}, database, fakeMigrator{err: errors.New("migration failed")})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	waitForAddress(t, application)
	assertHTTPStatus(t, "http://"+application.Addr()+"/healthz", http.StatusOK)
	assertHTTPStatus(t, "http://"+application.Addr()+"/readyz", http.StatusServiceUnavailable)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	values := map[string]string{
		"ORG_ENVIRONMENT":                  "test",
		"ORG_HTTP_ADDR":                    "127.0.0.1:0",
		"ORG_SHUTDOWN_TIMEOUT":             "2s",
		"ORG_LOG_FORMAT":                   "text",
		"ORG_DATABASE_MIGRATION_TIMEOUT":   "100ms",
		"ORG_DATABASE_PING_TIMEOUT":        "50ms",
		"ORG_DATABASE_HEALTH_CHECK_PERIOD": "50ms",
	}
	cfg, err := config.LoadFrom(func(key string) (string, bool) { value, ok := values[key]; return value, ok })
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}
	return cfg
}

func waitForAddress(t *testing.T, application *App) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for application.Addr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("application did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForStatus(t *testing.T, url string, status int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		response, err := http.Get(url)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == status {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not return %d", url, status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertHTTPStatus(t *testing.T, url string, want int) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("GET %s status = %d, want %d", url, response.StatusCode, want)
	}
}
