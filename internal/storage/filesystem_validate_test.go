package storage

import (
	"errors"
	"testing"
)

// TestValidateObjectKey_ControlCharsAndBackslash covers CR-4: reject control characters,
// backslash, and normalisation-collision patterns.
func TestValidateObjectKey_ControlCharsAndBackslash(t *testing.T) {
	fs := newTestFileSystem(t)

	cases := []struct {
		name string
		key  string
	}{
		{"null byte", "key\x00name"},
		{"carriage return", "key\rname"},
		{"newline", "key\nname"},
		{"backslash", "key\\name"},
		{"dot-slash prefix", "./victim"},
		{"slash-dot suffix", "victim/."},
		{"double slash", "victim//x"},
		{"dot traversal classic", "../etc/passwd"},
		{"dot in path component", "a/../b"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := fs.validateObjectKey("mybucket", tc.key)
			if !errors.Is(err, ErrInvalidKey) {
				t.Errorf("validateObjectKey(%q) = %v, want ErrInvalidKey", tc.key, err)
			}
		})
	}
}

// TestValidateObjectKey_ValidKeys verifies that normal keys still pass validation.
func TestValidateObjectKey_ValidKeys(t *testing.T) {
	fs := newTestFileSystem(t)

	cases := []string{
		"simple",
		"path/to/object",
		"unicode-日本語",
		"space in key",
		"file.txt",
		"nested/deep/path/file.bin",
	}

	for _, key := range cases {
		key := key
		t.Run(key, func(t *testing.T) {
			_, err := fs.validateObjectKey("mybucket", key)
			if err != nil {
				t.Errorf("validateObjectKey(%q) = %v, want nil", key, err)
			}
		})
	}
}
