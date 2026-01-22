// Package config provides configuration management for JOG server.
package config

import (
	"strings"

	"github.com/spf13/viper"
)

// Config holds the server configuration.
type Config struct {
	Server  ServerConfig  `mapstructure:"server"`
	Storage StorageConfig `mapstructure:"storage"`
	Auth    AuthConfig    `mapstructure:"auth"`
	Logging LoggingConfig `mapstructure:"logging"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port    int    `mapstructure:"port"`
	Address string `mapstructure:"address"`
}

// StorageConfig holds storage backend settings.
type StorageConfig struct {
	DataDir    string `mapstructure:"data_dir"`
	MetadataDB string `mapstructure:"metadata_db"`
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
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

// Load reads configuration from environment variables and config file.
func Load() (*Config, error) {
	cfg := DefaultConfig()

	v := viper.New()

	// Set defaults
	v.SetDefault("server.port", cfg.Server.Port)
	v.SetDefault("server.address", cfg.Server.Address)
	v.SetDefault("storage.data_dir", cfg.Storage.DataDir)
	v.SetDefault("storage.metadata_db", cfg.Storage.MetadataDB)
	v.SetDefault("auth.access_key", cfg.Auth.AccessKey)
	v.SetDefault("auth.secret_key", cfg.Auth.SecretKey)
	v.SetDefault("logging.level", cfg.Logging.Level)
	v.SetDefault("logging.format", cfg.Logging.Format)

	// Enable environment variables
	v.SetEnvPrefix("JOG")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Read config file if exists
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("/etc/jog")
	v.AddConfigPath("$HOME/.jog")

	if err := v.ReadInConfig(); err != nil {
		// Config file is optional
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// LoadFromFile reads configuration from a specific file.
func LoadFromFile(path string) (*Config, error) {
	cfg := DefaultConfig()

	v := viper.New()
	v.SetConfigFile(path)

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
