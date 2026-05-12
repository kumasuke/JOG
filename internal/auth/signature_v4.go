// Package auth provides AWS Signature V4 authentication.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kumasuke/jog/internal/api"
)

// hmacCompareKey is a process-local random key used by constantTimeHexEqual
// to fold both inputs into fixed-size HMAC digests before constant-time
// comparison (H-2). The key is unknown to attackers and changes per process,
// so the digest of "provided" reveals no information about its length or
// contents that could be correlated across runs.
var hmacCompareKey [32]byte

func init() {
	if _, err := io.ReadFull(rand.Reader, hmacCompareKey[:]); err != nil {
		panic("auth: failed to seed constant-time compare key: " + err.Error())
	}
}

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
		// H-5: CORS preflight requests cannot carry SigV4 credentials
		// (browsers strip them on OPTIONS), so requiring auth here would
		// make any cross-origin access impossible even for buckets with
		// a permissive CORS policy. Bypass only the narrow signature:
		// OPTIONS with both Origin and Access-Control-Request-Method.
		if isCORSPreflight(r) {
			next.ServeHTTP(w, r)
			return
		}

		// Check for Authorization header
		auth := r.Header.Get("Authorization")
		var ctx *sigCtx
		var s3err *api.S3Error
		if auth == "" {
			// Check for query string auth (presigned URL)
			if r.URL.Query().Get("X-Amz-Algorithm") == "" {
				api.WriteError(w, api.ErrAccessDenied)
				return
			}
			ctx, s3err = m.verifyPresignedURL(r)
		} else {
			ctx, s3err = m.verifySignatureV4(r, auth)
		}
		if s3err != nil {
			api.WriteError(w, s3err)
			return
		}

		// CR-2/CR-3 + H-1: wrap r.Body so the payload is bound to the
		// verified signature. wrapPayload returns the request unchanged
		// if the payload hash is UNSIGNED-PAYLOAD or the body is empty.
		// For presigned URLs this only kicks in when the client included
		// X-Amz-Content-SHA256 in SignedHeaders (otherwise ctx's payload
		// hash is empty and wrapPayload short-circuits).
		if ctx != nil && r.Body != nil && r.ContentLength != 0 {
			if err := wrapPayload(r, ctx); err != nil {
				api.WriteError(w, err)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// sigCtx carries the per-request information needed to bind the payload
// (request body) to the verified signature for CR-2/CR-3.
type sigCtx struct {
	seedSig    string // signature from the Authorization header
	signingKey []byte // derived kSigning
	amzDate    string // e.g. "20260102T030405Z"
	scope      string // e.g. "20260102/us-east-1/s3/aws4_request"
	// payloadHashHeader is the literal X-Amz-Content-SHA256 value sent by
	// the client: either "UNSIGNED-PAYLOAD", "STREAMING-AWS4-HMAC-SHA256-PAYLOAD",
	// or a 64-char hex sha256.
	payloadHashHeader string
}

// verifySignatureV4 verifies AWS Signature V4 authentication and returns
// the signature context required to bind the request body to the signature.
func (m *Middleware) verifySignatureV4(r *http.Request, auth string) (*sigCtx, *api.S3Error) {
	// Parse Authorization header
	// Format: AWS4-HMAC-SHA256 Credential=ACCESS_KEY/DATE/REGION/s3/aws4_request, SignedHeaders=..., Signature=...
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		return nil, api.ErrAccessDenied
	}

	// Parse components
	// Authorization header format varies by client:
	// - AWS SDK: "Credential=..., SignedHeaders=..., Signature=..." (comma + space)
	// - minio-go: "Credential=...,SignedHeaders=...,Signature=..." (comma only)
	authContent := strings.TrimPrefix(auth, "AWS4-HMAC-SHA256 ")
	authParams := make(map[string]string)

	// Split by comma, then trim spaces from each part
	parts := strings.Split(authContent, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			authParams[kv[0]] = kv[1]
		}
	}

	credential := authParams["Credential"]
	signedHeaders := authParams["SignedHeaders"]
	providedSignature := authParams["Signature"]

	if credential == "" || signedHeaders == "" || providedSignature == "" {
		return nil, api.ErrAccessDenied
	}

	// Parse credential: ACCESS_KEY/DATE/REGION/SERVICE/aws4_request
	credParts := strings.Split(credential, "/")
	if len(credParts) != 5 {
		return nil, api.ErrAccessDenied
	}

	accessKey := credParts[0]
	date := credParts[1]
	region := credParts[2]
	service := credParts[3]

	// Verify access key
	if accessKey != m.accessKey {
		return nil, api.ErrInvalidAccessKeyId
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
		return nil, api.ErrAccessDenied
	}

	// Check if request is within 15 minutes
	if time.Since(reqTime).Abs() > 15*time.Minute {
		return nil, api.ErrRequestTimeTooSkewed
	}

	// Calculate expected signature
	expectedSignature := m.calculateSignature(r, date, region, service, signedHeaders)

	// Compare signatures (H-2: decode hex and compare raw bytes to avoid
	// leaking length information through hmac.Equal's length-mismatch
	// short-circuit on hex strings).
	if !constantTimeHexEqual(expectedSignature, providedSignature) {
		return nil, api.ErrSignatureDoesNotMatch
	}

	return &sigCtx{
		seedSig:           providedSignature,
		signingKey:        m.getSigningKey(date, region, service),
		amzDate:           amzDate,
		scope:             date + "/" + region + "/" + service + "/aws4_request",
		payloadHashHeader: r.Header.Get("X-Amz-Content-SHA256"),
	}, nil
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

	// Canonical URI - must use EscapedPath to match AWS SDK's signature calculation
	// Go's HTTP server decodes the URI automatically, but AWS Signature V4 uses the encoded form
	uri := r.URL.EscapedPath()
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

// payloadVerifyingReader wraps r.Body with an io.Reader that streams the
// body through a SHA-256 hasher and, at EOF, compares the digest to the
// expected hex value declared in X-Amz-Content-SHA256. A mismatch is
// surfaced as an error on the final Read so that downstream handlers
// abort instead of persisting unverified bytes (CR-3).
type payloadVerifyingReader struct {
	src      io.ReadCloser
	hasher   hashWriter
	expected string // lowercase hex
	done     bool
}

// hashWriter is the subset of hash.Hash we need; declared locally so this
// file does not pull in the "hash" package.
type hashWriter interface {
	io.Writer
	Sum(b []byte) []byte
}

func (p *payloadVerifyingReader) Read(b []byte) (int, error) {
	if p.done {
		return 0, io.EOF
	}
	n, err := p.src.Read(b)
	if n > 0 {
		p.hasher.Write(b[:n])
	}
	if err == io.EOF {
		p.done = true
		got := hex.EncodeToString(p.hasher.Sum(nil))
		if !constantTimeHexEqual(got, p.expected) {
			return n, api.ErrPayloadHashMismatch
		}
	}
	return n, err
}

func (p *payloadVerifyingReader) Close() error {
	return p.src.Close()
}

// IsPayloadHashMismatch reports whether err originated from a payload
// SHA-256 verification failure (CR-3). It is a thin wrapper around the
// canonical helper in package api so callers in package auth do not
// need to import api just for the predicate.
func IsPayloadHashMismatch(err error) bool {
	return api.IsPayloadHashMismatchErr(err)
}

// wrapPayload installs a body reader that binds the request payload to
// the verified signature. The exact wrapping depends on the value of the
// X-Amz-Content-SHA256 header:
//
//   - "UNSIGNED-PAYLOAD": no wrapping (client opted out of payload signing).
//   - "STREAMING-AWS4-HMAC-SHA256-PAYLOAD": wrap with a signed ChunkedReader
//     that decodes aws-chunked framing and verifies each chunk's signature
//     chained from the seed signature (CR-2).
//   - 64-char hex: wrap with a streaming SHA-256 verifier that fails the
//     final Read if the digest does not match (CR-3).
//   - empty / unknown: no wrapping (preserves existing behaviour for
//     clients that omit the header; the canonical request still includes
//     "UNSIGNED-PAYLOAD" in that case).
func wrapPayload(r *http.Request, ctx *sigCtx) *api.S3Error {
	payloadHash := ctx.payloadHashHeader
	switch {
	case payloadHash == "" || payloadHash == "UNSIGNED-PAYLOAD":
		return nil
	case payloadHash == "STREAMING-AWS4-HMAC-SHA256-PAYLOAD":
		decoded := api.NewChunkedReader(r.Body, ctx.seedSig, ctx.signingKey, ctx.amzDate, ctx.scope)
		r.Body = io.NopCloser(decoded)
		return nil
	case len(payloadHash) == 64 && isHex(payloadHash):
		r.Body = &payloadVerifyingReader{
			src:      r.Body,
			hasher:   sha256.New(),
			expected: payloadHash,
		}
		return nil
	default:
		// Unknown payload-hash sentinel: be conservative and reject.
		return api.ErrSignatureDoesNotMatch
	}
}

// isHex reports whether s consists entirely of lower-case hex digits.
func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// constantTimeHexEqual reports whether two hex-encoded strings represent
// the same value, in a way that does not leak the length of "provided"
// via timing (H-2).
//
// Naive `expected == provided` and `hmac.Equal(decoded(expected),
// decoded(provided))` both short-circuit when input lengths differ:
// hmac.Equal is subtle.ConstantTimeCompare which returns 0 immediately
// when len(a) != len(b). For SigV4 expected is always 64 hex chars, so a
// length-mismatched provided value can be distinguished from a same-
// length wrong value by timing alone.
//
// We sidestep the early-return by hashing both inputs through HMAC-SHA256
// with a process-local random key, producing fixed 32-byte digests, and
// comparing those. The HMAC computation itself is roughly linear in input
// length, but it processes the attacker-controlled provided string only,
// so the only information leakable through timing is `len(provided)` —
// which the attacker already knows. Comparison is then over equal-length
// digests, eliminating the length-mismatch leak.
//
// Comparison is case-insensitive: SigV4 canonicalises signatures to
// lower-case hex, but accepting upper-case keeps clients that use
// strings.ToUpper-style helpers working without a separate length check.
func constantTimeHexEqual(expected, provided string) bool {
	h1 := hmac.New(sha256.New, hmacCompareKey[:])
	h1.Write([]byte(strings.ToLower(expected)))
	h2 := hmac.New(sha256.New, hmacCompareKey[:])
	h2.Write([]byte(strings.ToLower(provided)))
	return hmac.Equal(h1.Sum(nil), h2.Sum(nil))
}

// verifyPresignedURL verifies a presigned URL.
func (m *Middleware) verifyPresignedURL(r *http.Request) (*sigCtx, *api.S3Error) {
	query := r.URL.Query()

	algorithm := query.Get("X-Amz-Algorithm")
	if algorithm != "AWS4-HMAC-SHA256" {
		return nil, api.ErrAccessDenied
	}

	credential := query.Get("X-Amz-Credential")
	signedHeaders := query.Get("X-Amz-SignedHeaders")
	signature := query.Get("X-Amz-Signature")
	amzDate := query.Get("X-Amz-Date")
	expires := query.Get("X-Amz-Expires")

	// H-1: X-Amz-Expires is required for presigned URLs. Missing or
	// non-numeric values must not silently disable the expiry check.
	if credential == "" || signedHeaders == "" || signature == "" || amzDate == "" || expires == "" {
		return nil, api.ErrAccessDenied
	}

	// Parse credential
	credParts := strings.Split(credential, "/")
	if len(credParts) != 5 {
		return nil, api.ErrAccessDenied
	}

	accessKey := credParts[0]
	date := credParts[1]
	region := credParts[2]
	service := credParts[3]

	if accessKey != m.accessKey {
		return nil, api.ErrInvalidAccessKeyId
	}

	// H-1: Validate X-Amz-Expires. AWS requires an integer in (0, 604800]
	// (1 second to 7 days). Anything else is a malformed request.
	expiresSec, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || expiresSec <= 0 || expiresSec > 604800 {
		return nil, api.ErrAccessDenied
	}

	// Check expiration window
	reqTime, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil {
		return nil, api.ErrAccessDenied
	}

	now := time.Now().UTC()
	// H-1: Reject requests whose Date is more than 15 minutes in the
	// future. AWS enforces clock-skew bounds on both sides.
	if reqTime.Sub(now) > 15*time.Minute {
		return nil, api.ErrRequestTimeTooSkewed
	}
	if now.Sub(reqTime) > time.Duration(expiresSec)*time.Second {
		return nil, api.ErrRequestTimeTooSkewed
	}

	// Create canonical request for presigned URL
	// Remove signature from query for verification
	cleanQuery := r.URL.Query()
	cleanQuery.Del("X-Amz-Signature")
	r.URL.RawQuery = cleanQuery.Encode()

	expectedSignature := m.calculatePresignedSignature(r, date, region, service, signedHeaders, amzDate)

	if !constantTimeHexEqual(expectedSignature, signature) {
		return nil, api.ErrSignatureDoesNotMatch
	}

	// H-1 (hardening): a body-bearing presigned method (PUT / POST / PATCH)
	// must explicitly bind the payload by signing x-amz-content-sha256.
	// Without it the canonical request silently fell back to
	// "UNSIGNED-PAYLOAD" and the server cannot tell whether the client
	// meant to opt out of payload signing or simply forgot — leaving the
	// body unbound and allowing arbitrary content to be uploaded against
	// a "signed" URL. The header value itself may be either a 64-hex
	// SHA-256 digest (binds to a specific payload) or the literal
	// "UNSIGNED-PAYLOAD" (an explicit opt-out that is itself signed),
	// but the choice must appear in SignedHeaders so it cannot be
	// rewritten by the caller.
	if isBodyBearingPresignedMethod(r.Method) && !containsSignedHeader(signedHeaders, "x-amz-content-sha256") {
		return nil, api.ErrAccessDenied
	}

	// H-1: when the client included X-Amz-Content-SHA256 in SignedHeaders
	// the canonical request bound the body to the signature; propagate the
	// header value so the Middleware.Wrap path can install a payload
	// verifier (CR-3) for non-UNSIGNED-PAYLOAD hex digests.
	payloadHashHeader := ""
	if containsSignedHeader(signedHeaders, "x-amz-content-sha256") {
		payloadHashHeader = r.Header.Get("X-Amz-Content-SHA256")
	}

	return &sigCtx{
		seedSig:           signature,
		signingKey:        m.getSigningKey(date, region, service),
		amzDate:           amzDate,
		scope:             date + "/" + region + "/" + service + "/aws4_request",
		payloadHashHeader: payloadHashHeader,
	}, nil
}

// isBodyBearingPresignedMethod reports whether the HTTP method routinely
// carries a request body whose contents must be bound to a presigned
// URL's signature. GET / HEAD / DELETE / OPTIONS bodies are either
// disallowed or ignored by S3, so leaving them outside the strict
// payload-binding requirement avoids breaking presigned download URLs.
func isBodyBearingPresignedMethod(method string) bool {
	switch method {
	case http.MethodPut, http.MethodPost, http.MethodPatch:
		return true
	}
	return false
}

// isCORSPreflight reports whether r is a CORS preflight request. A
// preflight is OPTIONS + Origin + Access-Control-Request-Method (the
// trio browsers always send together). A bare OPTIONS without these
// headers does not qualify and still requires authentication.
func isCORSPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions &&
		r.Header.Get("Origin") != "" &&
		r.Header.Get("Access-Control-Request-Method") != ""
}

// containsSignedHeader reports whether name (lower-case) appears in the
// semicolon-separated SignedHeaders list. SignedHeaders is canonicalized
// to lower-case by all AWS SDKs.
func containsSignedHeader(signedHeaders, name string) bool {
	for _, h := range strings.Split(signedHeaders, ";") {
		if strings.EqualFold(strings.TrimSpace(h), name) {
			return true
		}
	}
	return false
}

// calculatePresignedSignature calculates signature for presigned URL.
func (m *Middleware) calculatePresignedSignature(r *http.Request, date, region, service, signedHeaders, amzDate string) string {
	// Create canonical request
	method := r.Method
	// Use EscapedPath to match AWS SDK's signature calculation for presigned URLs
	uri := r.URL.EscapedPath()
	if uri == "" {
		uri = "/"
	}
	queryString := m.canonicalQueryString(r)

	// Canonical headers
	headersList := strings.Split(signedHeaders, ";")
	sort.Strings(headersList)

	var canonicalHeaders strings.Builder
	signedContentSHA := ""
	for _, h := range headersList {
		h = strings.ToLower(h)
		var value string
		if h == "host" {
			value = r.Host
		} else {
			value = r.Header.Get(h)
		}
		if h == "x-amz-content-sha256" {
			signedContentSHA = strings.TrimSpace(value)
		}
		canonicalHeaders.WriteString(h)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(value))
		canonicalHeaders.WriteString("\n")
	}

	// H-1: if the client included X-Amz-Content-SHA256 in SignedHeaders the
	// canonical request must use the header value (binding the payload to
	// the signature); otherwise AWS defaults to UNSIGNED-PAYLOAD for query
	// string auth.
	payloadHash := "UNSIGNED-PAYLOAD"
	if signedContentSHA != "" {
		payloadHash = signedContentSHA
	}

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
