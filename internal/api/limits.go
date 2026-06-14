package api

import (
	"errors"
	"io"
	"net/http"
)

// Body size limits for XML endpoints (mirrors AWS S3 limits).
const (
	MaxACLBodySize           int64 = 256 * 1024      // 256 KiB
	MaxCORSBodySize          int64 = 64 * 1024       // 64 KiB
	MaxLifecycleBodySize     int64 = 256 * 1024      // 256 KiB
	MaxTaggingBodySize       int64 = 64 * 1024       // 64 KiB
	MaxVersioningBodySize    int64 = 4 * 1024        // 4 KiB
	MaxEncryptionBodySize    int64 = 16 * 1024       // 16 KiB
	MaxObjectLockBodySize    int64 = 16 * 1024       // 16 KiB
	MaxNotificationBodySize  int64 = 64 * 1024       // 64 KiB
	MaxSelectRequestBodySize int64 = 256 * 1024      // 256 KiB (SelectObjectContent SQL request)
	MaxWebsiteBodySize       int64 = 16 * 1024       // 16 KiB
	MaxMultipartCompleteSize int64 = 1 * 1024 * 1024 // 1 MiB (10001 parts * ~100 B)
	MaxDeleteObjectsSize     int64 = 2 * 1024 * 1024 // 2 MiB (1000 keys * ~2 KB)

	// H-11: per-request body upper bounds for object data. Mirrors AWS S3's
	// documented limits so a single client cannot exhaust local disk by
	// declaring a huge Content-Length. The check fires before the body is
	// streamed so storage is never touched for oversized requests.
	MaxPutObjectSize  int64 = 5 * 1024 * 1024 * 1024 // 5 GiB per single PUT
	MaxUploadPartSize int64 = 5 * 1024 * 1024 * 1024 // 5 GiB per UploadPart
)

// limitBody wraps r.Body with http.MaxBytesReader.
// When the client sends more than n bytes, subsequent reads return an error
// and net/http automatically sends a 413 status before the handler can write.
func limitBody(w http.ResponseWriter, r *http.Request, n int64) {
	r.Body = http.MaxBytesReader(w, r.Body, n)
}

// isBodyTooLarge returns true when err is the error returned by
// http.MaxBytesReader after the configured limit is exceeded.
func isBodyTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// readXMLBody caps r.Body at maxBytes and reads it fully, returning the bytes
// for the caller to unmarshal. It consolidates the limit-then-read-then-classify
// boilerplate that every XML subresource handler (ACL/CORS/encryption/lifecycle/
// notification/object-lock/tagging/versioning) previously duplicated.
//
// On success it returns the body and a nil error. When the body exceeds maxBytes
// it returns ErrEntityTooLarge; any other read failure returns ErrInvalidRequest.
// The S3Error is returned (not written) so each caller can attach its own
// resource path (e.g. "/"+bucket vs "/"+bucket+"/"+key) when responding.
func readXMLBody(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]byte, *S3Error) {
	limitBody(w, r, maxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		if isBodyTooLarge(err) {
			return nil, ErrEntityTooLarge
		}
		return nil, ErrInvalidRequest
	}
	return body, nil
}
