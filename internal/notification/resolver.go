package notification

// Resolver maps a notification target ARN (as stored in the bucket's
// notification configuration) to a concrete webhook URL configured on the
// server. This is the JOG analogue of MinIO's per-server notification targets:
// the bucket config names a target by ARN, the server config says where that ARN
// actually delivers. It decouples the S3-facing API (ARN-only) from the delivery
// mechanism (a URL the operator controls).
type Resolver struct {
	// arnToURL maps an exact ARN string to a webhook URL.
	arnToURL map[string]string
}

// NewResolver builds a resolver from an ARN→URL map. A nil/empty map resolves
// nothing (every Resolve returns ok=false).
func NewResolver(arnToURL map[string]string) *Resolver {
	m := make(map[string]string, len(arnToURL))
	for k, v := range arnToURL {
		m[k] = v
	}
	return &Resolver{arnToURL: m}
}

// Resolve returns the webhook URL mapped to arn, or ok=false when the ARN has no
// mapping (the dispatcher then drops the event with a warning).
func (r *Resolver) Resolve(arn string) (string, bool) {
	if r == nil {
		return "", false
	}
	url, ok := r.arnToURL[arn]
	return url, ok && url != ""
}
