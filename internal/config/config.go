package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAppName           = "explorarte-organization"
	defaultEnvironment       = "development"
	defaultHTTPAddr          = ":8080"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout   = 15 * time.Second
	defaultLogFormat         = "json"
)

type Config struct {
	App     AppConfig
	HTTP    HTTPConfig
	Logging LoggingConfig
}

type AppConfig struct {
	Name            string
	Environment     string
	ShutdownTimeout time.Duration
}

type HTTPConfig struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

type LoggingConfig struct {
	Level  slog.Level
	Format string
}

type LookupEnv func(string) (string, bool)

func Load() (Config, error) {
	return LoadFrom(os.LookupEnv)
}

func LoadFrom(lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("environment lookup cannot be nil")
	}

	shutdownTimeout, err := duration(lookup, "ORG_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}
	readHeaderTimeout, err := duration(lookup, "ORG_HTTP_READ_HEADER_TIMEOUT", defaultReadHeaderTimeout)
	if err != nil {
		return Config{}, err
	}
	readTimeout, err := duration(lookup, "ORG_HTTP_READ_TIMEOUT", defaultReadTimeout)
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := duration(lookup, "ORG_HTTP_WRITE_TIMEOUT", defaultWriteTimeout)
	if err != nil {
		return Config{}, err
	}
	idleTimeout, err := duration(lookup, "ORG_HTTP_IDLE_TIMEOUT", defaultIdleTimeout)
	if err != nil {
		return Config{}, err
	}
	level, err := logLevel(lookup)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		App: AppConfig{
			Name:            text(lookup, "ORG_APP_NAME", defaultAppName),
			Environment:     text(lookup, "ORG_ENVIRONMENT", defaultEnvironment),
			ShutdownTimeout: shutdownTimeout,
		},
		HTTP: HTTPConfig{
			Addr:              text(lookup, "ORG_HTTP_ADDR", defaultHTTPAddr),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
		},
		Logging: LoggingConfig{
			Level:  level,
			Format: strings.ToLower(text(lookup, "ORG_LOG_FORMAT", defaultLogFormat)),
		},
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (cfg Config) Validate() error {
	if strings.TrimSpace(cfg.App.Name) == "" {
		return errors.New("ORG_APP_NAME cannot be empty")
	}
	if strings.TrimSpace(cfg.App.Environment) == "" {
		return errors.New("ORG_ENVIRONMENT cannot be empty")
	}
	if cfg.App.ShutdownTimeout <= 0 {
		return errors.New("ORG_SHUTDOWN_TIMEOUT must be greater than zero")
	}

	if err := validateAddr(cfg.HTTP.Addr); err != nil {
		return fmt.Errorf("ORG_HTTP_ADDR: %w", err)
	}
	if cfg.HTTP.ReadHeaderTimeout <= 0 {
		return errors.New("ORG_HTTP_READ_HEADER_TIMEOUT must be greater than zero")
	}
	if cfg.HTTP.ReadTimeout <= 0 {
		return errors.New("ORG_HTTP_READ_TIMEOUT must be greater than zero")
	}
	if cfg.HTTP.WriteTimeout <= 0 {
		return errors.New("ORG_HTTP_WRITE_TIMEOUT must be greater than zero")
	}
	if cfg.HTTP.IdleTimeout <= 0 {
		return errors.New("ORG_HTTP_IDLE_TIMEOUT must be greater than zero")
	}

	switch cfg.Logging.Format {
	case "json", "text":
	default:
		return fmt.Errorf("ORG_LOG_FORMAT must be json or text, got %q", cfg.Logging.Format)
	}

	return nil
}

func text(lookup LookupEnv, key, fallback string) string {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func duration(lookup LookupEnv, key string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}

	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s: parse duration: %w", key, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}

	return parsed, nil
}

func logLevel(lookup LookupEnv) (slog.Level, error) {
	raw := strings.ToLower(text(lookup, "ORG_LOG_LEVEL", "info"))
	switch raw {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("ORG_LOG_LEVEL must be debug, info, warn, or error, got %q", raw)
	}
}

func validateAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("expected host:port: %w", err)
	}
	_ = host

	number, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("invalid port %q: %w", port, err)
	}
	if number < 0 || number > 65535 {
		return fmt.Errorf("port must be between 0 and 65535, got %d", number)
	}

	return nil
}
