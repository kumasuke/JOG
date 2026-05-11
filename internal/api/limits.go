package api

import (
	"errors"
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
	MaxWebsiteBodySize       int64 = 16 * 1024       // 16 KiB
	MaxMultipartCompleteSize int64 = 1 * 1024 * 1024 // 1 MiB (10001 parts * ~100 B)
	MaxDeleteObjectsSize     int64 = 2 * 1024 * 1024 // 2 MiB (1000 keys * ~2 KB)
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
