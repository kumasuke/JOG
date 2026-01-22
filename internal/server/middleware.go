package server

import (
	"net/http"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/kumasuke/jog/internal/api"
	"github.com/rs/zerolog/log"
)

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader atomic.Bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader.Swap(true) {
		return
	}
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.wroteHeader.CompareAndSwap(false, true) {
		rw.status = http.StatusOK
		rw.ResponseWriter.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// LoggingMiddleware logs HTTP requests.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		log.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Str("query", r.URL.RawQuery).
			Int("status", rw.status).
			Dur("duration", time.Since(start)).
			Str("remote", r.RemoteAddr).
			Msg("Request")
	})
}

// RecoveryMiddleware recovers from panics and returns 500 error.
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Error().
					Interface("error", err).
					Str("stack", string(debug.Stack())).
					Msg("Panic recovered")

				api.WriteError(w, api.ErrInternalError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
