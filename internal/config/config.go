package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var organizationIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:[-_][a-z0-9]+)*$`)

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

	defaultDatabaseHost                       = "127.0.0.1"
	defaultDatabasePort                       = 5432
	defaultDatabaseName                       = "explorarte_org"
	defaultDatabaseUser                       = "explorarte_app"
	defaultDatabaseSSLMode                    = "disable"
	defaultDatabaseMaxConns             int32 = 8
	defaultDatabaseMinConns             int32 = 1
	defaultDatabaseMaxConnLifetime            = 30 * time.Minute
	defaultDatabaseMaxConnIdleTime            = 5 * time.Minute
	defaultDatabaseHealthCheckPeriod          = 30 * time.Second
	defaultDatabaseConnectTimeout             = 5 * time.Second
	defaultDatabasePingTimeout                = 2 * time.Second
	defaultDatabaseStatementTimeout           = 30 * time.Second
	defaultDatabaseLockTimeout                = 5 * time.Second
	defaultDatabaseMigrationTimeout           = 45 * time.Second
	defaultDatabaseMigrationRetry             = 5 * time.Second
	defaultCanonicalDir                       = "docs/canonical"
	defaultRegistrySyncTimeout                = 30 * time.Second
	defaultTaskOrganizationID                 = "explorarte"
	defaultTaskReconcileInterval              = 5 * time.Second
	defaultTaskReconcileBatchSize             = 100
	defaultTaskDefaultMaxAttempts             = 5
	defaultTaskDefaultLeaseDuration           = 2 * time.Minute
	defaultTaskMaxLeaseDuration               = 15 * time.Minute
	defaultTaskRetryBaseDelay                 = 5 * time.Second
	defaultTaskRetryMaxDelay                  = 10 * time.Minute
	defaultTaskOutboxMaxAttempts              = 10
	defaultTaskOutboxClaimDuration            = time.Minute
	defaultTaskCommandTimeout                 = 30 * time.Second
	defaultAuthorizationDefaultTTL            = 30 * time.Minute
	defaultAuthorizationMaxTTL                = 24 * time.Hour
	defaultAuthorizationCommandTimeout        = 30 * time.Second
	defaultAuthorizationExpireBatchSize       = 100
	defaultContextSourceRoot                  = "/opt/explorarte/organization"
	defaultContextCommandTimeout              = 30 * time.Second
	defaultContextMaxTotalBytes               = 524288
	defaultContextMaxSegmentBytes             = 65536
	defaultContextMaxSegments                 = 128
	defaultContextMaxSkills                   = 16
	defaultContextMaxMemorySegments           = 32
	defaultContextMaxRAGSegments              = 20
	defaultStagingRepositoriesFile            = "/etc/explorarte/repositories.yaml"
	defaultStagingWorkspaceRoot               = "/var/lib/explorarte/staging/workspaces"
	defaultStagingArtifactRoot                = "/var/lib/explorarte/staging/artifacts"
	defaultStagingQuarantineRoot              = "/var/lib/explorarte/staging/quarantine"
	defaultStagingCommandTimeout              = 2 * time.Minute
	defaultStagingMaxArtifactBytes      int64 = 64 << 20
	defaultStagingMaxChangedFiles             = 500
	defaultStagingStaleAfter                  = 30 * time.Minute
	defaultStagingReconcileInterval           = 30 * time.Second
	defaultStagingReconcileBatchSize          = 100
	defaultStagingGitBinary                   = "git"
)

type Config struct {
	App           AppConfig
	HTTP          HTTPConfig
	Logging       LoggingConfig
	Database      DatabaseConfig
	Registry      RegistryConfig
	Tasks         TaskConfig
	Authorization AuthorizationConfig
	Context       ContextConfig
	Staging       StagingConfig
	ModelRuntime  ModelRuntimeConfig
}

// ModelRuntimeConfig holds test-only overrides for model routing/egress. All
// fields default to production-safe values and must be explicitly enabled.
type ModelRuntimeConfig struct {
	// SingleProviderTestMode relaxes the R24 executive egress scope gate so
	// that a single openai_compatible provider can satisfy the CEO, leader,
	// and worker scopes for a smoke test. Off by default; production
	// canonical routing and the gate itself are unaffected unless this is
	// explicitly set via ORG_MODEL_SINGLE_PROVIDER_TEST=true.
	SingleProviderTestMode bool
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

type RegistryConfig struct {
	CanonicalDir string
	SyncTimeout  time.Duration
}

type TaskConfig struct {
	OrganizationID       string
	ReconcilerEnabled    bool
	ReconcileInterval    time.Duration
	ReconcileBatchSize   int
	DefaultMaxAttempts   int
	DefaultLeaseDuration time.Duration
	MaxLeaseDuration     time.Duration
	RetryBaseDelay       time.Duration
	RetryMaxDelay        time.Duration
	OutboxMaxAttempts    int
	OutboxClaimDuration  time.Duration
	CommandTimeout       time.Duration
}

type AuthorizationConfig struct {
	DefaultTTL      time.Duration
	MaxTTL          time.Duration
	CommandTimeout  time.Duration
	ExpireBatchSize int
}

type ContextConfig struct {
	SourceRoot        string
	CommandTimeout    time.Duration
	MaxTotalBytes     int
	MaxSegmentBytes   int
	MaxSegments       int
	MaxSkills         int
	MaxMemorySegments int
	MaxRAGSegments    int
}

type StagingConfig struct {
	Enabled            bool
	RepositoriesFile   string
	WorkspaceRoot      string
	ArtifactRoot       string
	QuarantineRoot     string
	CommandTimeout     time.Duration
	MaxArtifactBytes   int64
	MaxChangedFiles    int
	StaleAfter         time.Duration
	ReconcileInterval  time.Duration
	ReconcileBatchSize int
	GitBinary          string
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
	registryTimeout, err := duration(lookup, "ORG_REGISTRY_SYNC_TIMEOUT", defaultRegistrySyncTimeout)
	if err != nil {
		return Config{}, err
	}
	canonicalDir := defaultCanonicalDir
	if raw, ok := lookup("ORG_CANONICAL_DIR"); ok {
		canonicalDir = strings.TrimSpace(raw)
	}
	tasks, err := loadTasks(lookup)
	if err != nil {
		return Config{}, err
	}
	authorization, err := loadAuthorization(lookup)
	if err != nil {
		return Config{}, err
	}
	contextConfig, err := loadContext(lookup)
	if err != nil {
		return Config{}, err
	}
	staging, err := loadStaging(lookup)
	if err != nil {
		return Config{}, err
	}
	singleProviderTestMode, err := boolean(lookup, "ORG_MODEL_SINGLE_PROVIDER_TEST", false)
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
		Database:      database,
		Registry:      RegistryConfig{CanonicalDir: canonicalDir, SyncTimeout: registryTimeout},
		Tasks:         tasks,
		Authorization: authorization,
		Context:       contextConfig,
		Staging:       staging,
		ModelRuntime:  ModelRuntimeConfig{SingleProviderTestMode: singleProviderTestMode},
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

func loadTasks(lookup LookupEnv) (TaskConfig, error) {
	reconcilerEnabled, err := boolean(lookup, "ORG_TASK_RECONCILER_ENABLED", true)
	if err != nil {
		return TaskConfig{}, err
	}
	reconcileInterval, err := duration(lookup, "ORG_TASK_RECONCILE_INTERVAL", defaultTaskReconcileInterval)
	if err != nil {
		return TaskConfig{}, err
	}
	reconcileBatch, err := integer(lookup, "ORG_TASK_RECONCILE_BATCH_SIZE", defaultTaskReconcileBatchSize, 1, 1000)
	if err != nil {
		return TaskConfig{}, err
	}
	maxAttempts, err := integer(lookup, "ORG_TASK_DEFAULT_MAX_ATTEMPTS", defaultTaskDefaultMaxAttempts, 1, 100)
	if err != nil {
		return TaskConfig{}, err
	}
	leaseDuration, err := duration(lookup, "ORG_TASK_DEFAULT_LEASE_DURATION", defaultTaskDefaultLeaseDuration)
	if err != nil {
		return TaskConfig{}, err
	}
	maxLeaseDuration, err := duration(lookup, "ORG_TASK_MAX_LEASE_DURATION", defaultTaskMaxLeaseDuration)
	if err != nil {
		return TaskConfig{}, err
	}
	retryBase, err := duration(lookup, "ORG_TASK_RETRY_BASE_DELAY", defaultTaskRetryBaseDelay)
	if err != nil {
		return TaskConfig{}, err
	}
	retryMax, err := duration(lookup, "ORG_TASK_RETRY_MAX_DELAY", defaultTaskRetryMaxDelay)
	if err != nil {
		return TaskConfig{}, err
	}
	outboxAttempts, err := integer(lookup, "ORG_TASK_OUTBOX_MAX_ATTEMPTS", defaultTaskOutboxMaxAttempts, 1, 100)
	if err != nil {
		return TaskConfig{}, err
	}
	outboxClaim, err := duration(lookup, "ORG_TASK_OUTBOX_CLAIM_DURATION", defaultTaskOutboxClaimDuration)
	if err != nil {
		return TaskConfig{}, err
	}
	commandTimeout, err := duration(lookup, "ORG_TASK_COMMAND_TIMEOUT", defaultTaskCommandTimeout)
	if err != nil {
		return TaskConfig{}, err
	}
	return TaskConfig{
		OrganizationID:       text(lookup, "ORG_TASK_ORGANIZATION_ID", defaultTaskOrganizationID),
		ReconcilerEnabled:    reconcilerEnabled,
		ReconcileInterval:    reconcileInterval,
		ReconcileBatchSize:   reconcileBatch,
		DefaultMaxAttempts:   maxAttempts,
		DefaultLeaseDuration: leaseDuration,
		MaxLeaseDuration:     maxLeaseDuration,
		RetryBaseDelay:       retryBase,
		RetryMaxDelay:        retryMax,
		OutboxMaxAttempts:    outboxAttempts,
		OutboxClaimDuration:  outboxClaim,
		CommandTimeout:       commandTimeout,
	}, nil
}

func loadAuthorization(lookup LookupEnv) (AuthorizationConfig, error) {
	defaultTTL, err := duration(lookup, "ORG_AUTHORIZATION_DEFAULT_TTL", defaultAuthorizationDefaultTTL)
	if err != nil {
		return AuthorizationConfig{}, err
	}
	maxTTL, err := duration(lookup, "ORG_AUTHORIZATION_MAX_TTL", defaultAuthorizationMaxTTL)
	if err != nil {
		return AuthorizationConfig{}, err
	}
	commandTimeout, err := duration(lookup, "ORG_AUTHORIZATION_COMMAND_TIMEOUT", defaultAuthorizationCommandTimeout)
	if err != nil {
		return AuthorizationConfig{}, err
	}
	batch, err := integer(lookup, "ORG_AUTHORIZATION_EXPIRE_BATCH_SIZE", defaultAuthorizationExpireBatchSize, 1, 1000)
	if err != nil {
		return AuthorizationConfig{}, err
	}
	return AuthorizationConfig{DefaultTTL: defaultTTL, MaxTTL: maxTTL, CommandTimeout: commandTimeout, ExpireBatchSize: batch}, nil
}

func loadContext(lookup LookupEnv) (ContextConfig, error) {
	timeout, err := duration(lookup, "ORG_CONTEXT_COMMAND_TIMEOUT", defaultContextCommandTimeout)
	if err != nil {
		return ContextConfig{}, err
	}
	maxTotal, err := integer(lookup, "ORG_CONTEXT_MAX_TOTAL_BYTES", defaultContextMaxTotalBytes, 64<<10, 4<<20)
	if err != nil {
		return ContextConfig{}, err
	}
	maxSegment, err := integer(lookup, "ORG_CONTEXT_MAX_SEGMENT_BYTES", defaultContextMaxSegmentBytes, 1<<10, maxTotal)
	if err != nil {
		return ContextConfig{}, err
	}
	maxSegments, err := integer(lookup, "ORG_CONTEXT_MAX_SEGMENTS", defaultContextMaxSegments, 1, 1000)
	if err != nil {
		return ContextConfig{}, err
	}
	maxSkills, err := integer(lookup, "ORG_CONTEXT_MAX_SKILLS", defaultContextMaxSkills, 0, 100)
	if err != nil {
		return ContextConfig{}, err
	}
	maxMemory, err := integer(lookup, "ORG_CONTEXT_MAX_MEMORY_SEGMENTS", defaultContextMaxMemorySegments, 0, 500)
	if err != nil {
		return ContextConfig{}, err
	}
	maxRAG, err := integer(lookup, "ORG_CONTEXT_MAX_RAG_SEGMENTS", defaultContextMaxRAGSegments, 0, 500)
	if err != nil {
		return ContextConfig{}, err
	}
	root := text(lookup, "ORG_CONTEXT_SOURCE_ROOT", defaultContextSourceRoot)
	if !filepath.IsAbs(root) {
		absolute, absErr := filepath.Abs(root)
		if absErr != nil {
			return ContextConfig{}, fmt.Errorf("ORG_CONTEXT_SOURCE_ROOT: %w", absErr)
		}
		root = absolute
	}
	return ContextConfig{SourceRoot: filepath.Clean(root), CommandTimeout: timeout, MaxTotalBytes: maxTotal, MaxSegmentBytes: maxSegment, MaxSegments: maxSegments, MaxSkills: maxSkills, MaxMemorySegments: maxMemory, MaxRAGSegments: maxRAG}, nil
}

func loadStaging(lookup LookupEnv) (StagingConfig, error) {
	enabled, err := boolean(lookup, "ORG_STAGING_ENABLED", false)
	if err != nil {
		return StagingConfig{}, err
	}
	commandTimeout, err := duration(lookup, "ORG_STAGING_COMMAND_TIMEOUT", defaultStagingCommandTimeout)
	if err != nil {
		return StagingConfig{}, err
	}
	maxArtifact, err := integer64(lookup, "ORG_STAGING_MAX_ARTIFACT_BYTES", defaultStagingMaxArtifactBytes, 1, 1<<40)
	if err != nil {
		return StagingConfig{}, err
	}
	maxChanged, err := integer(lookup, "ORG_STAGING_MAX_CHANGED_FILES", defaultStagingMaxChangedFiles, 1, 100000)
	if err != nil {
		return StagingConfig{}, err
	}
	staleAfter, err := duration(lookup, "ORG_STAGING_STALE_AFTER", defaultStagingStaleAfter)
	if err != nil {
		return StagingConfig{}, err
	}
	reconcileInterval, err := duration(lookup, "ORG_STAGING_RECONCILE_INTERVAL", defaultStagingReconcileInterval)
	if err != nil {
		return StagingConfig{}, err
	}
	batch, err := integer(lookup, "ORG_STAGING_RECONCILE_BATCH_SIZE", defaultStagingReconcileBatchSize, 1, 500)
	if err != nil {
		return StagingConfig{}, err
	}
	return StagingConfig{
		Enabled:            enabled,
		RepositoriesFile:   text(lookup, "ORG_STAGING_REPOSITORIES_FILE", defaultStagingRepositoriesFile),
		WorkspaceRoot:      text(lookup, "ORG_STAGING_WORKSPACE_ROOT", defaultStagingWorkspaceRoot),
		ArtifactRoot:       text(lookup, "ORG_STAGING_ARTIFACT_ROOT", defaultStagingArtifactRoot),
		QuarantineRoot:     text(lookup, "ORG_STAGING_QUARANTINE_ROOT", defaultStagingQuarantineRoot),
		CommandTimeout:     commandTimeout,
		MaxArtifactBytes:   maxArtifact,
		MaxChangedFiles:    maxChanged,
		StaleAfter:         staleAfter,
		ReconcileInterval:  reconcileInterval,
		ReconcileBatchSize: batch,
		GitBinary:          text(lookup, "ORG_STAGING_GIT_BINARY", defaultStagingGitBinary),
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
	if err := cfg.Database.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Registry.CanonicalDir) == "" {
		return errors.New("ORG_CANONICAL_DIR cannot be empty")
	}
	if cfg.Registry.SyncTimeout <= 0 {
		return errors.New("ORG_REGISTRY_SYNC_TIMEOUT must be greater than zero")
	}
	if err := cfg.Tasks.Validate(); err != nil {
		return err
	}
	if err := cfg.Authorization.Validate(); err != nil {
		return err
	}
	if err := cfg.Context.Validate(); err != nil {
		return err
	}
	if err := cfg.Staging.Validate(); err != nil {
		return err
	}
	return nil
}

func (cfg TaskConfig) Validate() error {
	if !organizationIDPattern.MatchString(strings.TrimSpace(cfg.OrganizationID)) {
		return errors.New("ORG_TASK_ORGANIZATION_ID must be a lowercase canonical identifier")
	}
	if cfg.ReconcileInterval <= 0 || cfg.DefaultLeaseDuration <= 0 || cfg.MaxLeaseDuration <= 0 || cfg.RetryBaseDelay <= 0 || cfg.RetryMaxDelay <= 0 || cfg.OutboxClaimDuration <= 0 || cfg.CommandTimeout <= 0 {
		return errors.New("task durations must be greater than zero")
	}
	if cfg.DefaultLeaseDuration > cfg.MaxLeaseDuration {
		return errors.New("ORG_TASK_DEFAULT_LEASE_DURATION cannot exceed ORG_TASK_MAX_LEASE_DURATION")
	}
	if cfg.RetryBaseDelay > cfg.RetryMaxDelay {
		return errors.New("ORG_TASK_RETRY_BASE_DELAY cannot exceed ORG_TASK_RETRY_MAX_DELAY")
	}
	if cfg.ReconcileBatchSize < 1 || cfg.ReconcileBatchSize > 1000 {
		return errors.New("ORG_TASK_RECONCILE_BATCH_SIZE must be between 1 and 1000")
	}
	if cfg.DefaultMaxAttempts < 1 || cfg.DefaultMaxAttempts > 100 {
		return errors.New("ORG_TASK_DEFAULT_MAX_ATTEMPTS must be between 1 and 100")
	}
	if cfg.OutboxMaxAttempts < 1 || cfg.OutboxMaxAttempts > 100 {
		return errors.New("ORG_TASK_OUTBOX_MAX_ATTEMPTS must be between 1 and 100")
	}
	return nil
}

func (cfg AuthorizationConfig) Validate() error {
	if cfg.DefaultTTL <= 0 {
		return errors.New("ORG_AUTHORIZATION_DEFAULT_TTL must be greater than zero")
	}
	if cfg.MaxTTL < cfg.DefaultTTL {
		return errors.New("ORG_AUTHORIZATION_MAX_TTL cannot be less than ORG_AUTHORIZATION_DEFAULT_TTL")
	}
	if cfg.CommandTimeout <= 0 {
		return errors.New("ORG_AUTHORIZATION_COMMAND_TIMEOUT must be greater than zero")
	}
	if cfg.ExpireBatchSize < 1 || cfg.ExpireBatchSize > 1000 {
		return errors.New("ORG_AUTHORIZATION_EXPIRE_BATCH_SIZE must be between 1 and 1000")
	}
	return nil
}

func (cfg ContextConfig) Validate() error {
	if strings.TrimSpace(cfg.SourceRoot) == "" || !filepath.IsAbs(cfg.SourceRoot) || filepath.Clean(cfg.SourceRoot) != cfg.SourceRoot || strings.ContainsRune(cfg.SourceRoot, 0) {
		return errors.New("ORG_CONTEXT_SOURCE_ROOT must be a non-empty absolute clean path")
	}
	if cfg.CommandTimeout <= 0 {
		return errors.New("ORG_CONTEXT_COMMAND_TIMEOUT must be greater than zero")
	}
	if cfg.MaxTotalBytes < 64<<10 || cfg.MaxTotalBytes > 4<<20 {
		return errors.New("ORG_CONTEXT_MAX_TOTAL_BYTES must be between 65536 and 4194304")
	}
	if cfg.MaxSegmentBytes < 1<<10 || cfg.MaxSegmentBytes > cfg.MaxTotalBytes {
		return errors.New("ORG_CONTEXT_MAX_SEGMENT_BYTES must be between 1024 and max total bytes")
	}
	if cfg.MaxSegments < 1 || cfg.MaxSegments > 1000 {
		return errors.New("ORG_CONTEXT_MAX_SEGMENTS must be between 1 and 1000")
	}
	if cfg.MaxSkills < 0 || cfg.MaxSkills > 100 {
		return errors.New("ORG_CONTEXT_MAX_SKILLS must be between 0 and 100")
	}
	if cfg.MaxMemorySegments < 0 || cfg.MaxMemorySegments > 500 {
		return errors.New("ORG_CONTEXT_MAX_MEMORY_SEGMENTS must be between 0 and 500")
	}
	if cfg.MaxRAGSegments < 0 || cfg.MaxRAGSegments > 500 {
		return errors.New("ORG_CONTEXT_MAX_RAG_SEGMENTS must be between 0 and 500")
	}
	return nil
}

func (cfg StagingConfig) Validate() error {
	if cfg.CommandTimeout <= 0 || cfg.StaleAfter <= 0 || cfg.ReconcileInterval <= 0 {
		return errors.New("staging durations must be greater than zero")
	}
	if cfg.MaxArtifactBytes <= 0 || cfg.MaxChangedFiles <= 0 {
		return errors.New("staging limits must be positive")
	}
	if cfg.ReconcileBatchSize < 1 || cfg.ReconcileBatchSize > 500 {
		return errors.New("ORG_STAGING_RECONCILE_BATCH_SIZE must be between 1 and 500")
	}
	if strings.TrimSpace(cfg.GitBinary) == "" || strings.ContainsAny(cfg.GitBinary, " \t\r\n") {
		return errors.New("ORG_STAGING_GIT_BINARY must be a binary without embedded arguments")
	}
	paths := []struct{ name, value string }{
		{"ORG_STAGING_REPOSITORIES_FILE", cfg.RepositoriesFile},
		{"ORG_STAGING_WORKSPACE_ROOT", cfg.WorkspaceRoot},
		{"ORG_STAGING_ARTIFACT_ROOT", cfg.ArtifactRoot},
		{"ORG_STAGING_QUARANTINE_ROOT", cfg.QuarantineRoot},
	}
	for _, item := range paths {
		if !filepath.IsAbs(item.value) || filepath.Clean(item.value) != item.value || strings.ContainsRune(item.value, 0) {
			return fmt.Errorf("%s must be an absolute clean path", item.name)
		}
	}
	roots := []string{cfg.WorkspaceRoot, cfg.ArtifactRoot, cfg.QuarantineRoot}
	for i := range roots {
		for j := i + 1; j < len(roots); j++ {
			rel, err := filepath.Rel(roots[i], roots[j])
			if err == nil && (rel == "." || rel == ".." || !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
				return errors.New("staging roots must not overlap")
			}
			rel, err = filepath.Rel(roots[j], roots[i])
			if err == nil && (rel == "." || rel == ".." || !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
				return errors.New("staging roots must not overlap")
			}
		}
	}
	return nil
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

func integer64(lookup LookupEnv, key string, fallback, minimum, maximum int64) (int64, error) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
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
