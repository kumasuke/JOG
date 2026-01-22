package testutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kumasuke/jog/internal/api"
	"github.com/kumasuke/jog/internal/auth"
	"github.com/kumasuke/jog/internal/server"
	"github.com/kumasuke/jog/internal/storage"
)

// TestServer provides a test JOG server instance.
type TestServer struct {
	t         *testing.T
	Endpoint  string
	AccessKey string
	SecretKey string
	DataDir   string

	listener net.Listener
	server   *http.Server
	storage  storage.Storage
}

// NewTestServer creates and starts a test server on a random port.
func NewTestServer(t *testing.T) *TestServer {
	t.Helper()

	// Create temp directory for data
	dataDir, err := os.MkdirTemp("", "jog-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	metadataDB := filepath.Join(dataDir, "metadata.db")

	// Initialize storage
	store, err := storage.NewFileSystem(dataDir, metadataDB)
	if err != nil {
		os.RemoveAll(dataDir)
		t.Fatalf("failed to create storage: %v", err)
	}

	// Create API handler
	apiHandler := api.NewHandler(store)

	// Create auth middleware (disabled for basic tests, can be enabled per-test)
	authMiddleware := auth.NewDisabledMiddleware()

	// Create router
	router := server.NewRouter(apiHandler, authMiddleware)

	// Wrap with logging and recovery
	handler := server.LoggingMiddleware(server.RecoveryMiddleware(router))

	// Find available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		store.Close()
		os.RemoveAll(dataDir)
		t.Fatalf("failed to find available port: %v", err)
	}

	srv := &http.Server{
		Handler: handler,
	}

	ts := &TestServer{
		t:         t,
		Endpoint:  fmt.Sprintf("http://%s", listener.Addr().String()),
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
		DataDir:   dataDir,
		listener:  listener,
		server:    srv,
		storage:   store,
	}

	// Start server in background
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			// Only log if not already cleaned up
			if ts.storage != nil {
				t.Logf("server error: %v", err)
			}
		}
	}()

	// Wait for server to be ready
	ts.waitForReady()

	return ts
}

// NewTestServerWithAuth creates a test server with authentication enabled.
func NewTestServerWithAuth(t *testing.T) *TestServer {
	t.Helper()

	// Create temp directory for data
	dataDir, err := os.MkdirTemp("", "jog-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	metadataDB := filepath.Join(dataDir, "metadata.db")
	accessKey := "minioadmin"
	secretKey := "minioadmin"

	// Initialize storage
	store, err := storage.NewFileSystem(dataDir, metadataDB)
	if err != nil {
		os.RemoveAll(dataDir)
		t.Fatalf("failed to create storage: %v", err)
	}

	// Create API handler
	apiHandler := api.NewHandler(store)

	// Create auth middleware with credentials
	authMiddleware := auth.NewMiddleware(accessKey, secretKey)

	// Create router
	router := server.NewRouter(apiHandler, authMiddleware)

	// Wrap with logging and recovery
	handler := server.LoggingMiddleware(server.RecoveryMiddleware(router))

	// Find available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		store.Close()
		os.RemoveAll(dataDir)
		t.Fatalf("failed to find available port: %v", err)
	}

	srv := &http.Server{
		Handler: handler,
	}

	ts := &TestServer{
		t:         t,
		Endpoint:  fmt.Sprintf("http://%s", listener.Addr().String()),
		AccessKey: accessKey,
		SecretKey: secretKey,
		DataDir:   dataDir,
		listener:  listener,
		server:    srv,
		storage:   store,
	}

	// Start server in background
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			if ts.storage != nil {
				t.Logf("server error: %v", err)
			}
		}
	}()

	// Wait for server to be ready
	ts.waitForReady()

	return ts
}

// waitForReady waits for the server to be ready.
func (ts *TestServer) waitForReady() {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(ts.Endpoint + "/")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	ts.t.Fatalf("server did not become ready")
}

// Cleanup stops the server and removes test data.
func (ts *TestServer) Cleanup() {
	if ts.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ts.server.Shutdown(ctx)
	}

	if ts.storage != nil {
		ts.storage.Close()
		ts.storage = nil
	}

	if ts.DataDir != "" {
		os.RemoveAll(ts.DataDir)
	}
}

// Storage returns the underlying storage for direct testing.
func (ts *TestServer) Storage() storage.Storage {
	return ts.storage
}
