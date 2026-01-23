package api

import (
	"bytes"
	"io"
	"testing"
)

func TestChunkedReader_SingleChunk(t *testing.T) {
	// Single chunk of 10 bytes
	// Format: <hex-size>;chunk-signature=<signature>\r\n<data>\r\n0;chunk-signature=<signature>\r\n\r\n
	data := "a;chunk-signature=abc123\r\n" +
		"0123456789\r\n" +
		"0;chunk-signature=def456\r\n" +
		"\r\n"

	reader := NewChunkedReader(bytes.NewReader([]byte(data)))
	result, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "0123456789"
	if string(result) != expected {
		t.Errorf("expected %q, got %q", expected, string(result))
	}
}

func TestChunkedReader_MultipleChunks(t *testing.T) {
	// Two chunks: 5 bytes + 5 bytes
	data := "5;chunk-signature=abc\r\n" +
		"hello\r\n" +
		"5;chunk-signature=def\r\n" +
		"world\r\n" +
		"0;chunk-signature=final\r\n" +
		"\r\n"

	reader := NewChunkedReader(bytes.NewReader([]byte(data)))
	result, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "helloworld"
	if string(result) != expected {
		t.Errorf("expected %q, got %q", expected, string(result))
	}
}

func TestChunkedReader_1KBChunk(t *testing.T) {
	// 1000 bytes in one chunk (like Warp benchmark)
	content := make([]byte, 1000)
	for i := range content {
		content[i] = byte(i % 256)
	}

	// 1000 in hex = 3e8
	data := "3e8;chunk-signature=abc123\r\n" +
		string(content) + "\r\n" +
		"0;chunk-signature=def456\r\n" +
		"\r\n"

	reader := NewChunkedReader(bytes.NewReader([]byte(data)))
	result, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1000 {
		t.Errorf("expected 1000 bytes, got %d bytes", len(result))
	}

	if !bytes.Equal(result, content) {
		t.Error("content mismatch")
	}
}

func TestChunkedReader_LargeChunks(t *testing.T) {
	// 64KB chunk (common AWS chunked size)
	content := make([]byte, 65536)
	for i := range content {
		content[i] = byte(i % 256)
	}

	// 65536 in hex = 10000
	data := "10000;chunk-signature=abc123\r\n" +
		string(content) + "\r\n" +
		"0;chunk-signature=def456\r\n" +
		"\r\n"

	reader := NewChunkedReader(bytes.NewReader([]byte(data)))
	result, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 65536 {
		t.Errorf("expected 65536 bytes, got %d bytes", len(result))
	}

	if !bytes.Equal(result, content) {
		t.Error("content mismatch")
	}
}

func TestChunkedReader_EmptyContent(t *testing.T) {
	// Empty content (0-size chunk only)
	data := "0;chunk-signature=abc123\r\n" +
		"\r\n"

	reader := NewChunkedReader(bytes.NewReader([]byte(data)))
	result, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 0 {
		t.Errorf("expected 0 bytes, got %d bytes", len(result))
	}
}

func TestChunkedReader_NoSignature(t *testing.T) {
	// Some clients might not include signature metadata
	data := "5\r\n" +
		"hello\r\n" +
		"0\r\n" +
		"\r\n"

	reader := NewChunkedReader(bytes.NewReader([]byte(data)))
	result, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "hello"
	if string(result) != expected {
		t.Errorf("expected %q, got %q", expected, string(result))
	}
}

func TestIsAWSChunked(t *testing.T) {
	tests := []struct {
		name            string
		contentEncoding string
		contentSHA256   string
		expected        bool
	}{
		{
			name:            "aws-chunked encoding",
			contentEncoding: "aws-chunked",
			contentSHA256:   "",
			expected:        true,
		},
		{
			name:            "streaming payload signature",
			contentEncoding: "",
			contentSHA256:   "STREAMING-AWS4-HMAC-SHA256-PAYLOAD",
			expected:        true,
		},
		{
			name:            "both headers",
			contentEncoding: "aws-chunked",
			contentSHA256:   "STREAMING-AWS4-HMAC-SHA256-PAYLOAD",
			expected:        true,
		},
		{
			name:            "regular request",
			contentEncoding: "",
			contentSHA256:   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expected:        false,
		},
		{
			name:            "gzip encoding",
			contentEncoding: "gzip",
			contentSHA256:   "",
			expected:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAWSChunked(tt.contentEncoding, tt.contentSHA256)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
