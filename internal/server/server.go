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
	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// Server represents the JOG HTTP server.
type Server struct {
	httpServer *http.Server
	storage    storage.Storage
	config     *config.Config
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
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Address, cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return &Server{
		httpServer: httpServer,
		storage:    store,
		config:     cfg,
	}, nil
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	log.Info().Str("addr", s.httpServer.Addr).Msg("Starting HTTP server")
	err := s.httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	log.Info().Msg("Shutting down server")

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
