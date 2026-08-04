package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/platform/buildinfo"
)

func TestHealthAndReadiness(t *testing.T) {
	ready := false
	server := New(
		config.HTTPConfig{
			Addr:              "127.0.0.1:0",
			ReadHeaderTimeout: time.Second,
			ReadTimeout:       time.Second,
			WriteTimeout:      time.Second,
			IdleTimeout:       time.Second,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		buildinfo.Info{Version: "test"},
		func() bool { return ready },
	)

	errorsCh, err := server.Start()
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		<-errorsCh
	})

	baseURL := "http://" + server.Addr()

	assertStatus(t, baseURL+"/healthz", http.StatusOK, "ok")
	assertStatus(t, baseURL+"/readyz", http.StatusServiceUnavailable, "not_ready")

	ready = true
	assertStatus(t, baseURL+"/readyz", http.StatusOK, "ready")
	assertStatus(t, baseURL+"/version", http.StatusOK, "")
}

func assertStatus(t *testing.T, url string, wantStatus int, wantState string) {
	t.Helper()

	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()

	if response.StatusCode != wantStatus {
		t.Fatalf("GET %s status = %d, want %d", url, response.StatusCode, wantStatus)
	}

	if wantState == "" {
		return
	}

	var payload struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Status != wantState {
		t.Fatalf("status = %q, want %q", payload.Status, wantState)
	}
}
