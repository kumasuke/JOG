package api

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"strconv"
	"strings"
)

// ErrChunkSignatureMismatch is returned by ChunkedReader when a chunk's
// signature does not match the value computed from the chained seed
// signature, signing key, and chunk payload. The HTTP layer maps this to
// SignatureDoesNotMatch.
var ErrChunkSignatureMismatch = errors.New("chunk signature mismatch")

// ErrInvalidChunkEncoding is returned when the aws-chunked framing itself
// is malformed (bad size, missing CRLF, etc.).
var ErrInvalidChunkEncoding = errors.New("invalid aws-chunked encoding")

// ErrPayloadHashMismatch is returned from a wrapped r.Body when the
// streamed bytes do not match the signed X-Amz-Content-SHA256 header
// (CR-3). It lives in this package so handlers and the auth middleware
// can both reference it without an import cycle.
var ErrPayloadHashMismatch = errors.New("payload hash does not match X-Amz-Content-SHA256")

// IsPayloadHashMismatchErr reports whether err originated from a payload
// SHA-256 verification failure (CR-3).
func IsPayloadHashMismatchErr(err error) bool {
	return errors.Is(err, ErrPayloadHashMismatch)
}

// emptyStringSHA256 is the SHA-256 hex digest of the empty string, used in
// the per-chunk string-to-sign as the previous-hash placeholder.
var emptyStringSHA256 = func() string {
	h := sha256.Sum256(nil)
	return hex.EncodeToString(h[:])
}()

// ChunkedReader decodes aws-chunked encoded request body and, when given
// a non-nil signing key, verifies the chained chunk-signature on each
// chunk per AWS SigV4 STREAMING-AWS4-HMAC-SHA256-PAYLOAD.
//
// AWS chunked format:
//
//	<hex-size>;chunk-signature=<signature>\r\n
//	<data>\r\n
//	...
//	0;chunk-signature=<final-signature>\r\n
//	\r\n
//
// For each chunk, the per-chunk string-to-sign is:
//
//	"AWS4-HMAC-SHA256-PAYLOAD" + "\n" +
//	amzDate                    + "\n" +
//	scope                      + "\n" +
//	previousSignature          + "\n" +
//	sha256_hex("")             + "\n" +
//	sha256_hex(chunk_payload)
//
// where previousSignature for the first chunk is the seed signature from
// the request's Authorization header.
type ChunkedReader struct {
	reader    *bufio.Reader
	remaining int64 // remaining payload bytes in current chunk
	done      bool

	// Signature verification state. If signingKey is nil, verification is
	// skipped (used by tests and by callers that have already verified
	// signatures upstream).
	signingKey []byte
	amzDate    string
	scope      string
	prevSig    string

	verify bool
	hasher hash.Hash // sha256 over the current chunk's payload bytes
	curSig string    // signature declared in the current chunk header
}

// NewChunkedReader creates a new ChunkedReader.
//
// If signingKey is non-nil, each chunk's signature is verified and chained
// from seedSig (the seed signature from the request's Authorization header)
// using amzDate (e.g. "20260102T030405Z") and scope (e.g.
// "20260102/us-east-1/s3/aws4_request"). Passing a nil signingKey disables
// verification (legacy / test usage).
func NewChunkedReader(r io.Reader, seedSig string, signingKey []byte, amzDate, scope string) *ChunkedReader {
	cr := &ChunkedReader{
		reader:     bufio.NewReader(r),
		signingKey: signingKey,
		amzDate:    amzDate,
		scope:      scope,
		prevSig:    seedSig,
		verify:     signingKey != nil,
	}
	if cr.verify {
		cr.hasher = sha256.New()
	}
	return cr
}

// Read implements io.Reader.
func (cr *ChunkedReader) Read(p []byte) (int, error) {
	if cr.done {
		return 0, io.EOF
	}

	// If no remaining bytes in current chunk, read next chunk header
	if cr.remaining == 0 {
		if err := cr.readChunkHeader(); err != nil {
			return 0, err
		}
		// Check if this is the final chunk (size 0)
		if cr.remaining == 0 {
			cr.done = true
			// Verify the terminating 0-byte chunk's signature, if needed.
			if cr.verify {
				if err := cr.verifyCurrentChunk(); err != nil {
					return 0, err
				}
			}
			// Read final CRLF after 0-size chunk
			_, _ = cr.reader.ReadString('\n')
			return 0, io.EOF
		}
	}

	// Read data from current chunk
	toRead := int64(len(p))
	if toRead > cr.remaining {
		toRead = cr.remaining
	}

	n, err := cr.reader.Read(p[:toRead])
	if n > 0 && cr.verify {
		cr.hasher.Write(p[:n])
	}
	cr.remaining -= int64(n)

	// If chunk is complete, read trailing CRLF and verify signature.
	if cr.remaining == 0 && n > 0 {
		// Read the \r\n after chunk data
		_, _ = cr.reader.ReadString('\n')
		if cr.verify {
			if vErr := cr.verifyCurrentChunk(); vErr != nil {
				return n, vErr
			}
		}
	}

	if err == io.EOF && !cr.done {
		// Unexpected EOF in the middle of chunked data
		return n, io.ErrUnexpectedEOF
	}

	return n, err
}

// readChunkHeader reads and parses the chunk header.
// Format: <hex-size>;chunk-signature=<signature>\r\n
func (cr *ChunkedReader) readChunkHeader() error {
	line, err := cr.reader.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			return io.ErrUnexpectedEOF
		}
		return err
	}

	// Remove trailing \r\n
	line = strings.TrimSuffix(line, "\r\n")
	line = strings.TrimSuffix(line, "\n")

	// Parse chunk size (before semicolon) and chunk-signature (after).
	semicolonIdx := strings.Index(line, ";")
	var sizeStr string
	var sig string
	if semicolonIdx >= 0 {
		sizeStr = line[:semicolonIdx]
		// Extract signature value if present.
		rest := line[semicolonIdx+1:]
		// rest looks like "chunk-signature=<sig>"; tolerate further ';' params.
		for _, part := range strings.Split(rest, ";") {
			part = strings.TrimSpace(part)
			const prefix = "chunk-signature="
			if strings.HasPrefix(part, prefix) {
				sig = strings.TrimSpace(part[len(prefix):])
				break
			}
		}
	} else {
		sizeStr = line
	}

	size, err := strconv.ParseInt(strings.TrimSpace(sizeStr), 16, 64)
	if err != nil || size < 0 {
		return ErrInvalidChunkEncoding
	}

	cr.remaining = size
	cr.curSig = sig
	return nil
}

// verifyCurrentChunk computes the expected chunk signature for the chunk
// just finished and compares it to the value declared in the chunk header.
// It also advances prevSig for the next chunk and resets the hasher.
func (cr *ChunkedReader) verifyCurrentChunk() error {
	if cr.curSig == "" {
		return ErrChunkSignatureMismatch
	}
	payloadHash := hex.EncodeToString(cr.hasher.Sum(nil))
	sts := "AWS4-HMAC-SHA256-PAYLOAD\n" +
		cr.amzDate + "\n" +
		cr.scope + "\n" +
		cr.prevSig + "\n" +
		emptyStringSHA256 + "\n" +
		payloadHash
	mac := hmac.New(sha256.New, cr.signingKey)
	mac.Write([]byte(sts))
	expectedRaw := mac.Sum(nil)
	providedRaw, decodeErr := hex.DecodeString(cr.curSig)
	if decodeErr != nil || !hmac.Equal(expectedRaw, providedRaw) {
		return ErrChunkSignatureMismatch
	}
	cr.prevSig = cr.curSig
	cr.hasher = sha256.New()
	return nil
}

// IsAWSChunked checks if the request uses aws-chunked encoding.
func IsAWSChunked(contentEncoding, contentSHA256 string) bool {
	// Check Content-Encoding header
	if strings.Contains(contentEncoding, "aws-chunked") {
		return true
	}
	// Also check X-Amz-Content-SHA256 header for streaming signature
	if contentSHA256 == "STREAMING-AWS4-HMAC-SHA256-PAYLOAD" {
		return true
	}
	return false
}
