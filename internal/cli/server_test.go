package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadSecretKeyFile_TrimsWhitespace verifies that a secret-key-file with
// a trailing newline (the natural form of `echo secret > file`) is read as
// the bare secret, not as "secret\n". A stray newline in the secret would
// break SigV4 verification with no useful error message.
func TestReadSecretKeyFile_TrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("my-super-secret\n"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	got, err := readSecretKeyFile(path)
	if err != nil {
		t.Fatalf("readSecretKeyFile() error = %v", err)
	}
	if got != "my-super-secret" {
		t.Fatalf("readSecretKeyFile() = %q, want %q", got, "my-super-secret")
	}
}

// TestReadSecretKeyFile_RejectsWorldReadable rejects a file with permissions
// looser than 0600 to prevent accidental disclosure (H-6).
func TestReadSecretKeyFile_RejectsWorldReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	_, err := readSecretKeyFile(path)
	if err == nil {
		t.Fatal("readSecretKeyFile() returned nil error for 0644 file; want permission error")
	}
	if !strings.Contains(err.Error(), "permission") {
		t.Fatalf("error = %v, want one mentioning 'permission'", err)
	}
}

// TestReadSecretKeyFile_RejectsEmpty rejects an empty file so misconfiguration
// is surfaced loudly instead of silently authenticating with "".
func TestReadSecretKeyFile_RejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}

	_, err := readSecretKeyFile(path)
	if err == nil {
		t.Fatal("readSecretKeyFile() returned nil error for empty file; want validation error")
	}
}
