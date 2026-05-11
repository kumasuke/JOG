package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

// buildSignedChunk returns the chunk header+body+CRLF for a signed aws-chunked stream
// and the computed signature (so callers can chain it).
func buildSignedChunk(t *testing.T, data, prevSig string, signingKey []byte, amzDate, scope string) (string, string) {
	t.Helper()
	emptyHash := sha256.Sum256(nil)
	dataHash := sha256.Sum256([]byte(data))
	sts := "AWS4-HMAC-SHA256-PAYLOAD\n" + amzDate + "\n" + scope + "\n" + prevSig + "\n" +
		hex.EncodeToString(emptyHash[:]) + "\n" + hex.EncodeToString(dataHash[:])
	mac := hmac.New(sha256.New, signingKey)
	mac.Write([]byte(sts))
	sig := hex.EncodeToString(mac.Sum(nil))
	header := ""
	// size is hex of len(data)
	if len(data) == 0 {
		header = "0;chunk-signature=" + sig + "\r\n"
		return header + "\r\n", sig
	}
	// hex without leading zeros
	hexSize := ""
	{
		n := len(data)
		buf := make([]byte, 0, 16)
		for n > 0 {
			d := n & 0xf
			if d < 10 {
				buf = append([]byte{byte('0' + d)}, buf...)
			} else {
				buf = append([]byte{byte('a' + d - 10)}, buf...)
			}
			n >>= 4
		}
		hexSize = string(buf)
	}
	header = hexSize + ";chunk-signature=" + sig + "\r\n"
	return header + data + "\r\n", sig
}

func TestChunkedReader_SingleChunk(t *testing.T) {
	// Single chunk of 10 bytes
	// Format: <hex-size>;chunk-signature=<signature>\r\n<data>\r\n0;chunk-signature=<signature>\r\n\r\n
	data := "a;chunk-signature=abc123\r\n" +
		"0123456789\r\n" +
		"0;chunk-signature=def456\r\n" +
		"\r\n"

	reader := NewChunkedReader(bytes.NewReader([]byte(data)), "", nil, "", "")
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

	reader := NewChunkedReader(bytes.NewReader([]byte(data)), "", nil, "", "")
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

	reader := NewChunkedReader(bytes.NewReader([]byte(data)), "", nil, "", "")
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

	reader := NewChunkedReader(bytes.NewReader([]byte(data)), "", nil, "", "")
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

	reader := NewChunkedReader(bytes.NewReader([]byte(data)), "", nil, "", "")
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

	reader := NewChunkedReader(bytes.NewReader([]byte(data)), "", nil, "", "")
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

// TestSignedChunkedReader_ChainedSignatures verifies that a chunk stream
// with correctly chained signatures is decoded successfully and that a
// tampered chunk signature is rejected (CR-2).
func TestSignedChunkedReader_ChainedSignatures(t *testing.T) {
	signingKey := []byte("test-signing-key")
	amzDate := "20260102T030405Z"
	scope := "20260102/us-east-1/s3/aws4_request"
	seedSig := "0000000000000000000000000000000000000000000000000000000000000001"

	// Build a 3-chunk stream: "hello", "world", "".
	c1, s1 := buildSignedChunk(t, "hello", seedSig, signingKey, amzDate, scope)
	c2, s2 := buildSignedChunk(t, "world", s1, signingKey, amzDate, scope)
	c3, _ := buildSignedChunk(t, "", s2, signingKey, amzDate, scope)
	stream := c1 + c2 + c3

	r := NewChunkedReader(bytes.NewReader([]byte(stream)), seedSig, signingKey, amzDate, scope)
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("io.ReadAll(signed chunks) returned %v", err)
	}
	if string(got) != "helloworld" {
		t.Fatalf("got %q, want %q", got, "helloworld")
	}
}

func TestSignedChunkedReader_TamperedSignatureIsRejected(t *testing.T) {
	signingKey := []byte("test-signing-key")
	amzDate := "20260102T030405Z"
	scope := "20260102/us-east-1/s3/aws4_request"
	seedSig := "0000000000000000000000000000000000000000000000000000000000000001"

	c1, _ := buildSignedChunk(t, "hello", seedSig, signingKey, amzDate, scope)
	// Replace the chunk-signature value with a deliberately wrong digest.
	// The 64-char zero string is a syntactically valid hex sig that won't match.
	semi := bytes.IndexByte([]byte(c1), ';')
	crlf := bytes.Index([]byte(c1), []byte("\r\n"))
	if semi < 0 || crlf < 0 {
		t.Fatalf("could not locate chunk header markers in %q", c1)
	}
	tampered := c1[:semi+1] + "chunk-signature=" + strings.Repeat("0", 64) + c1[crlf:]

	r := NewChunkedReader(bytes.NewReader([]byte(tampered)), seedSig, signingKey, amzDate, scope)
	_, err := io.ReadAll(r)
	if err == nil {
		t.Fatal("io.ReadAll(tampered) returned nil error; want chunk signature mismatch")
	}
	if !errors.Is(err, ErrChunkSignatureMismatch) {
		t.Fatalf("err = %v, want ErrChunkSignatureMismatch", err)
	}
}

// TestSignedChunkedReader_TamperedDataIsRejected verifies that flipping a
// byte in chunk payload (without changing the declared signature) is
// detected because the payload's SHA-256 will diverge.
func TestSignedChunkedReader_TamperedDataIsRejected(t *testing.T) {
	signingKey := []byte("test-signing-key")
	amzDate := "20260102T030405Z"
	scope := "20260102/us-east-1/s3/aws4_request"
	seedSig := "0000000000000000000000000000000000000000000000000000000000000001"

	c1, _ := buildSignedChunk(t, "hello", seedSig, signingKey, amzDate, scope)
	// Flip the first byte of payload: "hello" -> "Hello". The chunk
	// header (size + signature) is unchanged, so the only failure mode
	// is the SHA-256 of payload diverging from what was signed.
	b := []byte(c1)
	crlfIdx := bytes.Index(b, []byte("\r\n"))
	b[crlfIdx+2] = 'H'

	r := NewChunkedReader(bytes.NewReader(b), seedSig, signingKey, amzDate, scope)
	_, err := io.ReadAll(r)
	if err == nil {
		t.Fatal("io.ReadAll(tampered-data) returned nil error; want chunk signature mismatch")
	}
	if !errors.Is(err, ErrChunkSignatureMismatch) {
		t.Fatalf("err = %v, want ErrChunkSignatureMismatch", err)
	}
}
