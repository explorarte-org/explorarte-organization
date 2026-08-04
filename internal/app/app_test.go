package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/platform/buildinfo"
)

func TestRunStartsAndStopsCleanly(t *testing.T) {
	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		values := map[string]string{
			"ORG_ENVIRONMENT":      "test",
			"ORG_HTTP_ADDR":        "127.0.0.1:0",
			"ORG_SHUTDOWN_TIMEOUT": "2s",
			"ORG_LOG_FORMAT":       "text",
		}
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("LoadFrom() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	application := New(cfg, logger, buildinfo.Info{Version: "test"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- application.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !application.Ready() || application.Addr() == "" {
		if time.Now().After(deadline) {
			t.Fatal("application did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	response, err := http.Get("http://" + application.Addr() + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/readyz status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("application did not stop")
	}
}
