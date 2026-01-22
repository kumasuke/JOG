package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kumasuke/jog/internal/config"
	"github.com/kumasuke/jog/internal/server"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	configFile string
	port       int
	dataDir    string
	accessKey  string
	secretKey  string
	logLevel   string
)

// NewServerCmd creates the server command.
func NewServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start the S3-compatible server",
		Long:  "Start the JOG server that provides S3-compatible API endpoints.",
		RunE:  runServer,
	}

	cmd.Flags().StringVarP(&configFile, "config", "c", "", "config file path")
	cmd.Flags().IntVarP(&port, "port", "p", 0, "server port (default 9000)")
	cmd.Flags().StringVarP(&dataDir, "data-dir", "d", "", "data directory")
	cmd.Flags().StringVar(&accessKey, "access-key", "", "access key")
	cmd.Flags().StringVar(&secretKey, "secret-key", "", "secret key")
	cmd.Flags().StringVar(&logLevel, "log-level", "", "log level (debug, info, warn, error)")

	return cmd
}

func runServer(cmd *cobra.Command, args []string) error {
	// Load configuration
	var cfg *config.Config
	var err error

	if configFile != "" {
		cfg, err = config.LoadFromFile(configFile)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Override with command line flags
	if port != 0 {
		cfg.Server.Port = port
	}
	if dataDir != "" {
		cfg.Storage.DataDir = dataDir
	}
	if accessKey != "" {
		cfg.Auth.AccessKey = accessKey
	}
	if secretKey != "" {
		cfg.Auth.SecretKey = secretKey
	}
	if logLevel != "" {
		cfg.Logging.Level = logLevel
	}

	// Setup logging
	setupLogging(cfg.Logging)

	log.Info().
		Int("port", cfg.Server.Port).
		Str("data_dir", cfg.Storage.DataDir).
		Msg("Starting JOG server")

	// Create and start server
	srv, err := server.New(cfg)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		log.Info().Str("signal", sig.String()).Msg("Received shutdown signal")
		return srv.Shutdown()
	}
}

func setupLogging(cfg config.LoggingConfig) {
	// Set log level
	level, err := zerolog.ParseLevel(cfg.Level)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	// Set log format
	if cfg.Format == "console" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}
}
