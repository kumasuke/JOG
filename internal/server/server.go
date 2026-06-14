// Package server provides HTTP server for S3-compatible API.
package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/kumasuke/jog/internal/api"
	"github.com/kumasuke/jog/internal/auth"
	"github.com/kumasuke/jog/internal/config"
	"github.com/kumasuke/jog/internal/lifecycle"
	"github.com/kumasuke/jog/internal/notification"
	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// Server represents the JOG HTTP server.
type Server struct {
	httpServer *http.Server
	storage    storage.Storage
	config     *config.Config
	lifecycle  *lifecycle.Engine
	notifier   *notification.Dispatcher
	lcCancel   context.CancelFunc
	lcDone     chan struct{}
}

// New creates a new Server instance.
func New(cfg *config.Config) (*Server, error) {
	// Initialize storage
	store, err := storage.NewFileSystem(cfg.Storage.DataDir, cfg.Storage.MetadataDB)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize storage: %w", err)
	}

	// Create API handler
	apiHandler := api.NewHandler(store)

	// Create auth middleware
	authMiddleware := auth.NewMiddleware(cfg.Auth.AccessKey, cfg.Auth.SecretKey)

	// Create router
	router := NewRouter(apiHandler, authMiddleware)

	// Create HTTP server
	httpServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Address, cfg.Server.Port),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB
	}

	// Build the lifecycle engine over the same storage. It is only started in
	// Start() when cfg.Lifecycle.Enabled is true.
	lcEngine := lifecycle.NewEngine(store, lifecycle.Config{
		Interval:           cfg.Lifecycle.Interval.Std(),
		MaxActionsPerCycle: cfg.Lifecycle.MaxActionsPerCycle,
	}, time.Now)

	// Wire lifecycle-expiration notifications when webhook targets are configured.
	// FromTargets returns nil (notifications disabled) when no targets are set; in
	// that case we leave the engine's notifier unset rather than installing a
	// typed-nil interface. We retain disp on the Server so Shutdown can await any
	// in-flight async deliveries (Wait is nil-safe).
	disp := notification.FromTargets(
		cfg.Notification.Targets,
		cfg.Notification.Region,
		cfg.Notification.DeliveryTimeout.Std(),
	)
	if disp != nil {
		lcEngine.SetNotifier(disp)
		log.Info().Int("targets", len(cfg.Notification.Targets)).Msg("Lifecycle expiration notifications enabled (webhook)")
	}

	return &Server{
		httpServer: httpServer,
		storage:    store,
		config:     cfg,
		lifecycle:  lcEngine,
		notifier:   disp,
	}, nil
}

// Start starts the HTTP server. When lifecycle.enabled is set it first launches
// the lifecycle engine in a background goroutine, then serves HTTP.
func (s *Server) Start() error {
	if s.config.Lifecycle.Enabled {
		lcCtx, cancel := context.WithCancel(context.Background())
		s.lcCancel = cancel
		s.lcDone = make(chan struct{})
		go func() {
			defer close(s.lcDone)
			s.lifecycle.Run(lcCtx)
		}()
		log.Info().
			Dur("interval", s.config.Lifecycle.Interval.Std()).
			Int("max_actions_per_cycle", s.config.Lifecycle.MaxActionsPerCycle).
			Msg("Lifecycle engine enabled")
	} else {
		log.Info().Msg("Lifecycle engine disabled (lifecycle.enabled=false)")
	}

	log.Info().Str("addr", s.httpServer.Addr).Msg("Starting HTTP server")
	err := s.httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server. The lifecycle engine is stopped
// first (and awaited at a version-level boundary) so no guarded deletion is
// interrupted mid-flight; then any in-flight notification deliveries are
// awaited, and finally the HTTP server drains and storage closes.
func (s *Server) Shutdown() error {
	log.Info().Msg("Shutting down server")

	if s.lcCancel != nil {
		s.lcCancel()
		<-s.lcDone // wait for the engine to stop at a transaction boundary
	}

	// The engine has stopped, so no new events can be dispatched. Wait for any
	// async webhook deliveries already in flight so we don't drop them on exit.
	// Wait is nil-safe (no-op when notifications are disabled).
	s.notifier.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown error: %w", err)
	}

	if err := s.storage.Close(); err != nil {
		return fmt.Errorf("storage close error: %w", err)
	}

	return nil
}

// Storage returns the storage backend (for testing).
func (s *Server) Storage() storage.Storage {
	return s.storage
}
