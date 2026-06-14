// Package config provides configuration management for JOG server.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration so YAML/string values like "1h" parse via
// time.ParseDuration. yaml.v3 would otherwise reject the non-integer form.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Std returns the underlying time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// LifecycleConfig holds settings for the lifecycle execution engine.
type LifecycleConfig struct {
	Enabled            bool     `yaml:"enabled"`
	Interval           Duration `yaml:"interval"`
	MaxActionsPerCycle int      `yaml:"max_actions_per_cycle"`
}

// Config holds the server configuration.
type Config struct {
	Server       ServerConfig       `yaml:"server"`
	Storage      StorageConfig      `yaml:"storage"`
	Auth         AuthConfig         `yaml:"auth"`
	Logging      LoggingConfig      `yaml:"logging"`
	Lifecycle    LifecycleConfig    `yaml:"lifecycle"`
	Notification NotificationConfig `yaml:"notification"`
}

// NotificationConfig holds bucket-notification event delivery settings. v1
// delivers only via webhook (HTTP POST); SNS/SQS/Lambda/EventBridge are not yet
// implemented (Issue #56). A bucket's notification configuration names a target
// by ARN; Targets maps that ARN to the webhook URL the server actually POSTs to
// (the MinIO model). An ARN with no mapping is dropped (logged), so notifications
// are off by default until an operator wires a target here.
type NotificationConfig struct {
	// Region is stamped into each event's awsRegion field.
	Region string `yaml:"region"`
	// DeliveryTimeout caps a single webhook POST. Zero → 10s.
	DeliveryTimeout Duration `yaml:"delivery_timeout"`
	// Targets maps a notification ARN (TopicArn/QueueArn/LambdaFunctionArn from a
	// bucket's PutBucketNotification) to a webhook URL.
	Targets map[string]string `yaml:"targets"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port    int    `yaml:"port"`
	Address string `yaml:"address"`
}

// StorageConfig holds storage backend settings.
type StorageConfig struct {
	DataDir    string `yaml:"data_dir"`
	MetadataDB string `yaml:"metadata_db"`
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// DefaultConfig returns a Config with default values.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:    9000,
			Address: "0.0.0.0",
		},
		Storage: StorageConfig{
			DataDir:    "./data",
			MetadataDB: "./data/metadata.db",
		},
		Auth: AuthConfig{
			AccessKey: "minioadmin",
			SecretKey: "minioadmin",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Lifecycle: LifecycleConfig{
			Enabled:            true,
			Interval:           Duration(time.Hour),
			MaxActionsPerCycle: 10000,
		},
		Notification: NotificationConfig{
			Region:          "us-east-1",
			DeliveryTimeout: Duration(10 * time.Second),
			Targets:         map[string]string{},
		},
	}
}

// configSearchPaths returns the candidate paths for config.yaml in priority order.
func configSearchPaths() []string {
	paths := []string{
		"./config.yaml",
		"/etc/jog/config.yaml",
	}
	if home := os.Getenv("HOME"); home != "" {
		paths = append(paths, filepath.Join(home, ".jog", "config.yaml"))
	}
	return paths
}

// Load reads configuration from a YAML file (if found) and overlays
// environment variables on top. Precedence: env > file > defaults.
func Load() (*Config, error) {
	cfg := DefaultConfig()

	for _, path := range configSearchPaths() {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
		break
	}

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}

	if cfg.Auth.AccessKey == "minioadmin" {
		log.Warn().Msg("JOG_AUTH_ACCESS_KEY is using the default value 'minioadmin'; set it to a strong key in production")
	}

	return cfg, nil
}

// LoadFromFile reads configuration from a specific file. Environment
// variables are not consulted: this is the explicit "use exactly this file"
// path used by the `--config` CLI flag.
func LoadFromFile(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// applyEnv overlays JOG_* environment variables onto cfg. Each key maps
// directly to a struct field; precedence over file/defaults is enforced
// here by running last.
func applyEnv(cfg *Config) error {
	if err := envInt("JOG_SERVER_PORT", &cfg.Server.Port); err != nil {
		return err
	}
	envString("JOG_SERVER_ADDRESS", &cfg.Server.Address)
	envString("JOG_STORAGE_DATA_DIR", &cfg.Storage.DataDir)
	envString("JOG_STORAGE_METADATA_DB", &cfg.Storage.MetadataDB)
	envString("JOG_AUTH_ACCESS_KEY", &cfg.Auth.AccessKey)
	envString("JOG_AUTH_SECRET_KEY", &cfg.Auth.SecretKey)
	envString("JOG_LOGGING_LEVEL", &cfg.Logging.Level)
	envString("JOG_LOGGING_FORMAT", &cfg.Logging.Format)
	if err := envBool("JOG_LIFECYCLE_ENABLED", &cfg.Lifecycle.Enabled); err != nil {
		return err
	}
	if err := envDuration("JOG_LIFECYCLE_INTERVAL", &cfg.Lifecycle.Interval); err != nil {
		return err
	}
	if err := envInt("JOG_LIFECYCLE_MAX_ACTIONS", &cfg.Lifecycle.MaxActionsPerCycle); err != nil {
		return err
	}
	envString("JOG_NOTIFICATION_REGION", &cfg.Notification.Region)
	if err := envDuration("JOG_NOTIFICATION_DELIVERY_TIMEOUT", &cfg.Notification.DeliveryTimeout); err != nil {
		return err
	}
	// ARN→URL target mappings are file-only: a map does not map cleanly onto a
	// single env var, and operators set notification targets in config.yaml.
	return nil
}

// Empty values (`JOG_FOO=`) are treated as "unset" to match viper's
// historical behavior — otherwise an empty env var would clobber the
// default/file value with "" (or fail Atoi for int fields).
func envString(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func envInt(key string, dst *int) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("invalid value for %s=%q: %w", key, v, err)
	}
	*dst = n
	return nil
}

func envBool(key string, dst *bool) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fmt.Errorf("invalid value for %s=%q: %w", key, v, err)
	}
	*dst = b
	return nil
}

func envDuration(key string, dst *Duration) error {
	v := os.Getenv(key)
	if v == "" {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("invalid value for %s=%q: %w", key, v, err)
	}
	*dst = Duration(d)
	return nil
}
