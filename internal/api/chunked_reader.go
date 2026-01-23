package api

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
)

// ChunkedReader decodes aws-chunked encoded request body.
// AWS chunked format:
//
//	<hex-size>;chunk-signature=<signature>\r\n
//	<data>\r\n
//	...
//	0;chunk-signature=<final-signature>\r\n
//	\r\n
type ChunkedReader struct {
	reader    *bufio.Reader
	remaining int64 // remaining bytes in current chunk
	done      bool
}

// NewChunkedReader creates a new ChunkedReader.
func NewChunkedReader(r io.Reader) *ChunkedReader {
	return &ChunkedReader{
		reader: bufio.NewReader(r),
	}
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
	cr.remaining -= int64(n)

	// If chunk is complete, read trailing CRLF
	if cr.remaining == 0 && n > 0 {
		// Read the \r\n after chunk data
		_, _ = cr.reader.ReadString('\n')
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

	// Parse chunk size (before semicolon)
	semicolonIdx := strings.Index(line, ";")
	var sizeStr string
	if semicolonIdx >= 0 {
		sizeStr = line[:semicolonIdx]
	} else {
		sizeStr = line
	}

	size, err := strconv.ParseInt(sizeStr, 16, 64)
	if err != nil {
		return errors.New("invalid chunk size")
	}

	cr.remaining = size
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
