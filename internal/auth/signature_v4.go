// Package auth provides AWS Signature V4 authentication.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/kumasuke/jog/internal/api"
)

// Middleware handles AWS Signature V4 authentication.
type Middleware struct {
	accessKey string
	secretKey string
}

// NewMiddleware creates a new authentication middleware.
func NewMiddleware(accessKey, secretKey string) *Middleware {
	return &Middleware{
		accessKey: accessKey,
		secretKey: secretKey,
	}
}

// Wrap wraps an HTTP handler with authentication.
func (m *Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for Authorization header
		auth := r.Header.Get("Authorization")
		if auth == "" {
			// Check for query string auth (presigned URL)
			if r.URL.Query().Get("X-Amz-Algorithm") != "" {
				if err := m.verifyPresignedURL(r); err != nil {
					api.WriteError(w, err)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			api.WriteError(w, api.ErrAccessDenied)
			return
		}

		// Parse and verify AWS Signature V4
		if err := m.verifySignatureV4(r, auth); err != nil {
			api.WriteError(w, err)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// verifySignatureV4 verifies AWS Signature V4 authentication.
func (m *Middleware) verifySignatureV4(r *http.Request, auth string) *api.S3Error {
	// Parse Authorization header
	// Format: AWS4-HMAC-SHA256 Credential=ACCESS_KEY/DATE/REGION/s3/aws4_request, SignedHeaders=..., Signature=...
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		return api.ErrAccessDenied
	}

	// Parse components
	parts := strings.Split(strings.TrimPrefix(auth, "AWS4-HMAC-SHA256 "), ", ")
	authParams := make(map[string]string)
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			authParams[kv[0]] = kv[1]
		}
	}

	credential := authParams["Credential"]
	signedHeaders := authParams["SignedHeaders"]
	providedSignature := authParams["Signature"]

	if credential == "" || signedHeaders == "" || providedSignature == "" {
		return api.ErrAccessDenied
	}

	// Parse credential: ACCESS_KEY/DATE/REGION/SERVICE/aws4_request
	credParts := strings.Split(credential, "/")
	if len(credParts) != 5 {
		return api.ErrAccessDenied
	}

	accessKey := credParts[0]
	date := credParts[1]
	region := credParts[2]
	service := credParts[3]

	// Verify access key
	if accessKey != m.accessKey {
		return api.ErrInvalidAccessKeyId
	}

	// Get request date
	amzDate := r.Header.Get("X-Amz-Date")
	if amzDate == "" {
		amzDate = r.Header.Get("Date")
	}

	// Parse and verify date
	var reqTime time.Time
	var err error
	if strings.Contains(amzDate, "T") {
		reqTime, err = time.Parse("20060102T150405Z", amzDate)
	} else {
		reqTime, err = time.Parse(time.RFC1123, amzDate)
	}
	if err != nil {
		return api.ErrAccessDenied
	}

	// Check if request is within 15 minutes
	if time.Since(reqTime).Abs() > 15*time.Minute {
		return api.ErrRequestTimeTooSkewed
	}

	// Calculate expected signature
	expectedSignature := m.calculateSignature(r, date, region, service, signedHeaders)

	// Compare signatures
	if !hmac.Equal([]byte(expectedSignature), []byte(providedSignature)) {
		return api.ErrSignatureDoesNotMatch
	}

	return nil
}

// calculateSignature calculates AWS Signature V4.
func (m *Middleware) calculateSignature(r *http.Request, date, region, service, signedHeaders string) string {
	// Create canonical request
	canonicalRequest := m.createCanonicalRequest(r, signedHeaders)
	canonicalRequestHash := sha256Hash(canonicalRequest)

	// Create string to sign
	amzDate := r.Header.Get("X-Amz-Date")
	if amzDate == "" {
		amzDate = time.Now().UTC().Format("20060102T150405Z")
	}

	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + canonicalRequestHash

	// Calculate signing key
	signingKey := m.getSigningKey(date, region, service)

	// Calculate signature
	signature := hmacSHA256(signingKey, stringToSign)
	return hex.EncodeToString(signature)
}

// createCanonicalRequest creates the canonical request string.
func (m *Middleware) createCanonicalRequest(r *http.Request, signedHeaders string) string {
	// HTTP method
	method := r.Method

	// Canonical URI
	uri := r.URL.Path
	if uri == "" {
		uri = "/"
	}

	// Canonical query string
	queryString := m.canonicalQueryString(r)

	// Canonical headers
	headersList := strings.Split(signedHeaders, ";")
	sort.Strings(headersList)

	var canonicalHeaders strings.Builder
	for _, h := range headersList {
		h = strings.ToLower(h)
		var value string
		if h == "host" {
			value = r.Host
		} else {
			value = r.Header.Get(h)
		}
		canonicalHeaders.WriteString(h)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(value))
		canonicalHeaders.WriteString("\n")
	}

	// Payload hash
	payloadHash := r.Header.Get("X-Amz-Content-SHA256")
	if payloadHash == "" {
		payloadHash = "UNSIGNED-PAYLOAD"
	}

	return method + "\n" +
		uri + "\n" +
		queryString + "\n" +
		canonicalHeaders.String() + "\n" +
		signedHeaders + "\n" +
		payloadHash
}

// canonicalQueryString creates the canonical query string.
func (m *Middleware) canonicalQueryString(r *http.Request) string {
	query := r.URL.Query()
	if len(query) == 0 {
		return ""
	}

	// Sort keys
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		values := query[k]
		sort.Strings(values)
		for _, v := range values {
			parts = append(parts, uriEncode(k)+"="+uriEncode(v))
		}
	}

	return strings.Join(parts, "&")
}

// getSigningKey derives the signing key.
func (m *Middleware) getSigningKey(date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+m.secretKey), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	return kSigning
}

// verifyPresignedURL verifies a presigned URL.
func (m *Middleware) verifyPresignedURL(r *http.Request) *api.S3Error {
	query := r.URL.Query()

	algorithm := query.Get("X-Amz-Algorithm")
	if algorithm != "AWS4-HMAC-SHA256" {
		return api.ErrAccessDenied
	}

	credential := query.Get("X-Amz-Credential")
	signedHeaders := query.Get("X-Amz-SignedHeaders")
	signature := query.Get("X-Amz-Signature")
	amzDate := query.Get("X-Amz-Date")
	expires := query.Get("X-Amz-Expires")

	if credential == "" || signedHeaders == "" || signature == "" || amzDate == "" {
		return api.ErrAccessDenied
	}

	// Parse credential
	credParts := strings.Split(credential, "/")
	if len(credParts) != 5 {
		return api.ErrAccessDenied
	}

	accessKey := credParts[0]
	date := credParts[1]
	region := credParts[2]
	service := credParts[3]

	if accessKey != m.accessKey {
		return api.ErrInvalidAccessKeyId
	}

	// Check expiration
	reqTime, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil {
		return api.ErrAccessDenied
	}

	if expires != "" {
		expiresSec, err := time.ParseDuration(expires + "s")
		if err == nil {
			if time.Since(reqTime) > expiresSec {
				return api.ErrRequestTimeTooSkewed
			}
		}
	}

	// Create canonical request for presigned URL
	// Remove signature from query for verification
	cleanQuery := r.URL.Query()
	cleanQuery.Del("X-Amz-Signature")
	r.URL.RawQuery = cleanQuery.Encode()

	expectedSignature := m.calculatePresignedSignature(r, date, region, service, signedHeaders, amzDate)

	if !hmac.Equal([]byte(expectedSignature), []byte(signature)) {
		return api.ErrSignatureDoesNotMatch
	}

	return nil
}

// calculatePresignedSignature calculates signature for presigned URL.
func (m *Middleware) calculatePresignedSignature(r *http.Request, date, region, service, signedHeaders, amzDate string) string {
	// Create canonical request
	method := r.Method
	uri := r.URL.Path
	if uri == "" {
		uri = "/"
	}
	queryString := m.canonicalQueryString(r)

	// Canonical headers
	headersList := strings.Split(signedHeaders, ";")
	sort.Strings(headersList)

	var canonicalHeaders strings.Builder
	for _, h := range headersList {
		h = strings.ToLower(h)
		var value string
		if h == "host" {
			value = r.Host
		} else {
			value = r.Header.Get(h)
		}
		canonicalHeaders.WriteString(h)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(value))
		canonicalHeaders.WriteString("\n")
	}

	payloadHash := "UNSIGNED-PAYLOAD"

	canonicalRequest := method + "\n" +
		uri + "\n" +
		queryString + "\n" +
		canonicalHeaders.String() + "\n" +
		signedHeaders + "\n" +
		payloadHash

	canonicalRequestHash := sha256Hash(canonicalRequest)

	// String to sign
	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + canonicalRequestHash

	// Signing key
	signingKey := m.getSigningKey(date, region, service)

	// Signature
	signature := hmacSHA256(signingKey, stringToSign)
	return hex.EncodeToString(signature)
}

func sha256Hash(data string) string {
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func uriEncode(s string) string {
	// AWS URI encoding with proper UTF-8 support
	var result strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			result.WriteByte(c)
		} else {
			result.WriteString("%" + strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return result.String()
}

func isUnreserved(c byte) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_' || c == '.' || c == '~'
}

// DisabledMiddleware is a middleware that skips authentication (for testing).
type DisabledMiddleware struct{}

// NewDisabledMiddleware creates a middleware that skips authentication.
func NewDisabledMiddleware() *DisabledMiddleware {
	return &DisabledMiddleware{}
}

// Wrap wraps an HTTP handler without authentication.
func (m *DisabledMiddleware) Wrap(next http.Handler) http.Handler {
	return next
}

// Authenticator interface for different auth strategies.
type Authenticator interface {
	Wrap(next http.Handler) http.Handler
}

// Ensure implementations satisfy interface
var _ Authenticator = (*Middleware)(nil)
var _ Authenticator = (*DisabledMiddleware)(nil)
