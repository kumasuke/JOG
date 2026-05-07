// Package config provides configuration management for JOG server.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config holds the server configuration.
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Storage StorageConfig `yaml:"storage"`
	Auth    AuthConfig    `yaml:"auth"`
	Logging LoggingConfig `yaml:"logging"`
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
	return nil
}

func envString(key string, dst *string) {
	if v, ok := os.LookupEnv(key); ok {
		*dst = v
	}
}

func envInt(key string, dst *int) error {
	v, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("invalid value for %s=%q: %w", key, v, err)
	}
	*dst = n
	return nil
}
