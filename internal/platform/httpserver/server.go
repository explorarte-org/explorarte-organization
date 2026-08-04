package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/Mireuz13/explorarte-organization/internal/config"
	"github.com/Mireuz13/explorarte-organization/internal/platform/buildinfo"
)

type ReadyFunc func(context.Context) error

type Server struct {
	logger     *slog.Logger
	buildInfo  buildinfo.Info
	ready      ReadyFunc
	httpServer *http.Server
	mu         sync.RWMutex
	listener   net.Listener
}

func New(cfg config.HTTPConfig, logger *slog.Logger, info buildinfo.Info, ready ReadyFunc) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if ready == nil {
		ready = func(context.Context) error { return errors.New("readiness dependency is not configured") }
	}
	server := &Server{logger: logger, buildInfo: info, ready: ready}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.handleHealth)
	mux.HandleFunc("GET /readyz", server.handleReady)
	mux.HandleFunc("GET /version", server.handleVersion)
	server.httpServer = &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.recoverPanic(server.logRequest(mux)),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
	return server
}

func (s *Server) Start() (<-chan error, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return nil, errors.New("HTTP server already started")
	}
	listener, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", s.httpServer.Addr, err)
	}
	s.listener = listener
	errorsCh := make(chan error, 1)
	go func() {
		err := s.httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errorsCh <- err
		close(errorsCh)
	}()
	return errorsCh, nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	return nil
}

func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) handleHealth(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleReady(response http.ResponseWriter, request *http.Request) {
	if err := s.ready(request.Context()); err != nil {
		s.logger.Debug("readiness check failed", "error", err)
		writeJSON(response, http.StatusServiceUnavailable, map[string]any{
			"status":       "not_ready",
			"dependencies": map[string]string{"postgres": "unavailable"},
		})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"status":       "ready",
		"dependencies": map[string]string{"postgres": "ready"},
	})
}

func (s *Server) handleVersion(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, s.buildInfo)
}

func (s *Server) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(response, request)
		s.logger.Debug("HTTP request", "method", request.Method, "path", request.URL.Path, "remote_addr", request.RemoteAddr, "duration", time.Since(started))
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic in HTTP handler", "panic", recovered, "stack", string(debug.Stack()), "method", request.Method, "path", request.URL.Path)
				writeJSON(response, http.StatusInternalServerError, map[string]any{"status": "error"})
			}
		}()
		next.ServeHTTP(response, request)
	})
}

func writeJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(payload); err != nil {
		slog.Default().Error("encode HTTP response", "error", err)
	}
}
