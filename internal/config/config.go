package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
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

	defaultDatabaseHost                    = "127.0.0.1"
	defaultDatabasePort                    = 5432
	defaultDatabaseName                    = "explorarte_org"
	defaultDatabaseUser                    = "explorarte_app"
	defaultDatabaseSSLMode                 = "disable"
	defaultDatabaseMaxConns          int32 = 8
	defaultDatabaseMinConns          int32 = 1
	defaultDatabaseMaxConnLifetime         = 30 * time.Minute
	defaultDatabaseMaxConnIdleTime         = 5 * time.Minute
	defaultDatabaseHealthCheckPeriod       = 30 * time.Second
	defaultDatabaseConnectTimeout          = 5 * time.Second
	defaultDatabasePingTimeout             = 2 * time.Second
	defaultDatabaseStatementTimeout        = 30 * time.Second
	defaultDatabaseLockTimeout             = 5 * time.Second
	defaultDatabaseMigrationTimeout        = 45 * time.Second
	defaultDatabaseMigrationRetry          = 5 * time.Second
)

type Config struct {
	App      AppConfig
	HTTP     HTTPConfig
	Logging  LoggingConfig
	Database DatabaseConfig
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

type DatabaseConfig struct {
	URL               string
	Host              string
	Port              uint16
	Name              string
	User              string
	Password          string
	SSLMode           string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
	ConnectTimeout    time.Duration
	PingTimeout       time.Duration
	StatementTimeout  time.Duration
	LockTimeout       time.Duration
	AutoMigrate       bool
	MigrationTimeout  time.Duration
	MigrationRetry    time.Duration
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
	database, err := loadDatabase(lookup)
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
		Database: database,
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadDatabase(lookup LookupEnv) (DatabaseConfig, error) {
	port, err := integer(lookup, "ORG_DATABASE_PORT", defaultDatabasePort, 1, 65535)
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxConns, err := integer(lookup, "ORG_DATABASE_MAX_CONNS", int(defaultDatabaseMaxConns), 1, 100)
	if err != nil {
		return DatabaseConfig{}, err
	}
	minConns, err := integer(lookup, "ORG_DATABASE_MIN_CONNS", int(defaultDatabaseMinConns), 0, 100)
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxConnLifetime, err := duration(lookup, "ORG_DATABASE_MAX_CONN_LIFETIME", defaultDatabaseMaxConnLifetime)
	if err != nil {
		return DatabaseConfig{}, err
	}
	maxConnIdleTime, err := duration(lookup, "ORG_DATABASE_MAX_CONN_IDLE_TIME", defaultDatabaseMaxConnIdleTime)
	if err != nil {
		return DatabaseConfig{}, err
	}
	healthCheckPeriod, err := duration(lookup, "ORG_DATABASE_HEALTH_CHECK_PERIOD", defaultDatabaseHealthCheckPeriod)
	if err != nil {
		return DatabaseConfig{}, err
	}
	connectTimeout, err := duration(lookup, "ORG_DATABASE_CONNECT_TIMEOUT", defaultDatabaseConnectTimeout)
	if err != nil {
		return DatabaseConfig{}, err
	}
	pingTimeout, err := duration(lookup, "ORG_DATABASE_PING_TIMEOUT", defaultDatabasePingTimeout)
	if err != nil {
		return DatabaseConfig{}, err
	}
	statementTimeout, err := duration(lookup, "ORG_DATABASE_STATEMENT_TIMEOUT", defaultDatabaseStatementTimeout)
	if err != nil {
		return DatabaseConfig{}, err
	}
	lockTimeout, err := duration(lookup, "ORG_DATABASE_LOCK_TIMEOUT", defaultDatabaseLockTimeout)
	if err != nil {
		return DatabaseConfig{}, err
	}
	migrationTimeout, err := duration(lookup, "ORG_DATABASE_MIGRATION_TIMEOUT", defaultDatabaseMigrationTimeout)
	if err != nil {
		return DatabaseConfig{}, err
	}
	migrationRetry, err := duration(lookup, "ORG_DATABASE_MIGRATION_RETRY", defaultDatabaseMigrationRetry)
	if err != nil {
		return DatabaseConfig{}, err
	}
	autoMigrate, err := boolean(lookup, "ORG_DATABASE_AUTO_MIGRATE", true)
	if err != nil {
		return DatabaseConfig{}, err
	}

	return DatabaseConfig{
		URL:               optionalText(lookup, "ORG_DATABASE_URL"),
		Host:              text(lookup, "ORG_DATABASE_HOST", defaultDatabaseHost),
		Port:              uint16(port),
		Name:              text(lookup, "ORG_DATABASE_NAME", defaultDatabaseName),
		User:              text(lookup, "ORG_DATABASE_USER", defaultDatabaseUser),
		Password:          optionalText(lookup, "ORG_DATABASE_PASSWORD"),
		SSLMode:           strings.ToLower(text(lookup, "ORG_DATABASE_SSLMODE", defaultDatabaseSSLMode)),
		MaxConns:          int32(maxConns),
		MinConns:          int32(minConns),
		MaxConnLifetime:   maxConnLifetime,
		MaxConnIdleTime:   maxConnIdleTime,
		HealthCheckPeriod: healthCheckPeriod,
		ConnectTimeout:    connectTimeout,
		PingTimeout:       pingTimeout,
		StatementTimeout:  statementTimeout,
		LockTimeout:       lockTimeout,
		AutoMigrate:       autoMigrate,
		MigrationTimeout:  migrationTimeout,
		MigrationRetry:    migrationRetry,
	}, nil
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
	if cfg.HTTP.ReadHeaderTimeout <= 0 || cfg.HTTP.ReadTimeout <= 0 || cfg.HTTP.WriteTimeout <= 0 || cfg.HTTP.IdleTimeout <= 0 {
		return errors.New("HTTP timeouts must be greater than zero")
	}
	switch cfg.Logging.Format {
	case "json", "text":
	default:
		return fmt.Errorf("ORG_LOG_FORMAT must be json or text, got %q", cfg.Logging.Format)
	}
	return cfg.Database.Validate()
}

func (cfg DatabaseConfig) Validate() error {
	if cfg.URL != "" {
		parsed, err := url.Parse(cfg.URL)
		if err != nil {
			return fmt.Errorf("ORG_DATABASE_URL: %w", err)
		}
		if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
			return errors.New("ORG_DATABASE_URL must use postgres or postgresql scheme")
		}
		if parsed.Host == "" {
			return errors.New("ORG_DATABASE_URL must include a host")
		}
	} else {
		if strings.TrimSpace(cfg.Host) == "" {
			return errors.New("ORG_DATABASE_HOST cannot be empty")
		}
		if cfg.Port == 0 {
			return errors.New("ORG_DATABASE_PORT must be greater than zero")
		}
		if strings.TrimSpace(cfg.Name) == "" {
			return errors.New("ORG_DATABASE_NAME cannot be empty")
		}
		if strings.TrimSpace(cfg.User) == "" {
			return errors.New("ORG_DATABASE_USER cannot be empty")
		}
	}
	switch cfg.SSLMode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return fmt.Errorf("ORG_DATABASE_SSLMODE has unsupported value %q", cfg.SSLMode)
	}
	if cfg.MaxConns <= 0 {
		return errors.New("ORG_DATABASE_MAX_CONNS must be greater than zero")
	}
	if cfg.MinConns < 0 || cfg.MinConns > cfg.MaxConns {
		return errors.New("ORG_DATABASE_MIN_CONNS must be between zero and ORG_DATABASE_MAX_CONNS")
	}
	if cfg.MaxConnLifetime <= 0 || cfg.MaxConnIdleTime <= 0 || cfg.HealthCheckPeriod <= 0 || cfg.ConnectTimeout <= 0 || cfg.PingTimeout <= 0 || cfg.StatementTimeout <= 0 || cfg.LockTimeout <= 0 || cfg.MigrationTimeout <= 0 || cfg.MigrationRetry <= 0 {
		return errors.New("database durations must be greater than zero")
	}
	return nil
}

func (cfg DatabaseConfig) ConnectionString() string {
	if cfg.URL != "" {
		return cfg.URL
	}
	connection := &url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))),
		Path:   cfg.Name,
	}
	if cfg.Password == "" {
		connection.User = url.User(cfg.User)
	} else {
		connection.User = url.UserPassword(cfg.User, cfg.Password)
	}
	query := connection.Query()
	query.Set("sslmode", cfg.SSLMode)
	query.Set("connect_timeout", strconv.Itoa(max(1, int(cfg.ConnectTimeout/time.Second))))
	connection.RawQuery = query.Encode()
	return connection.String()
}

func text(lookup LookupEnv, key, fallback string) string {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func optionalText(lookup LookupEnv, key string) string {
	value, ok := lookup(key)
	if !ok {
		return ""
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

func integer(lookup LookupEnv, key string, fallback, minimum, maximum int) (int, error) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s: parse integer: %w", key, err)
	}
	if parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be between %d and %d", key, minimum, maximum)
	}
	return parsed, nil
}

func boolean(lookup LookupEnv, key string, fallback bool) (bool, error) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return false, fmt.Errorf("%s: parse boolean: %w", key, err)
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
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("expected host:port: %w", err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("invalid port %q: %w", port, err)
	}
	if number < 0 || number > 65535 {
		return fmt.Errorf("port must be between 0 and 65535, got %d", number)
	}
	return nil
}
