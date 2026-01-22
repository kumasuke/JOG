package api

import (
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// CORSConfiguration represents the XML structure for CORS configuration.
type CORSConfiguration struct {
	XMLName   xml.Name   `xml:"CORSConfiguration"`
	Xmlns     string     `xml:"xmlns,attr,omitempty"`
	CORSRules []CORSRule `xml:"CORSRule"`
}

// CORSRule represents a single CORS rule.
type CORSRule struct {
	AllowedOrigins []string `xml:"AllowedOrigin"`
	AllowedMethods []string `xml:"AllowedMethod"`
	AllowedHeaders []string `xml:"AllowedHeader,omitempty"`
	ExposeHeaders  []string `xml:"ExposeHeader,omitempty"`
	MaxAgeSeconds  *int32   `xml:"MaxAgeSeconds,omitempty"`
}

// PutBucketCors handles PUT /{bucket}?cors - PutBucketCors.
func (h *Handler) PutBucketCors(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	// Parse request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		WriteErrorWithResource(w, ErrInvalidRequest, "/"+bucket)
		return
	}

	var corsConfig CORSConfiguration
	if err := xml.Unmarshal(body, &corsConfig); err != nil {
		WriteErrorWithResource(w, ErrMalformedXML, "/"+bucket)
		return
	}

	// Convert to storage CORS configuration
	storageCors := &storage.CORSConfiguration{
		Rules: make([]storage.CORSRule, len(corsConfig.CORSRules)),
	}
	for i, rule := range corsConfig.CORSRules {
		storageCors.Rules[i] = storage.CORSRule{
			AllowedOrigins: rule.AllowedOrigins,
			AllowedMethods: rule.AllowedMethods,
			AllowedHeaders: rule.AllowedHeaders,
			ExposeHeaders:  rule.ExposeHeaders,
		}
		if rule.MaxAgeSeconds != nil {
			storageCors.Rules[i].MaxAgeSeconds = *rule.MaxAgeSeconds
		}
	}

	// Store CORS configuration
	err = h.storage.PutBucketCors(r.Context(), bucket, storageCors)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GetBucketCors handles GET /{bucket}?cors - GetBucketCors.
func (h *Handler) GetBucketCors(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	cors, err := h.storage.GetBucketCors(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrNoSuchCORSConfiguration) {
			WriteErrorWithResource(w, ErrNoSuchCORSConfiguration, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	// Build response
	response := CORSConfiguration{
		Xmlns:     "http://s3.amazonaws.com/doc/2006-03-01/",
		CORSRules: make([]CORSRule, len(cors.Rules)),
	}
	for i, rule := range cors.Rules {
		response.CORSRules[i] = CORSRule{
			AllowedOrigins: rule.AllowedOrigins,
			AllowedMethods: rule.AllowedMethods,
			AllowedHeaders: rule.AllowedHeaders,
			ExposeHeaders:  rule.ExposeHeaders,
		}
		if rule.MaxAgeSeconds > 0 {
			maxAge := rule.MaxAgeSeconds
			response.CORSRules[i].MaxAgeSeconds = &maxAge
		}
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetBucketCors response")
	}
}

// DeleteBucketCors handles DELETE /{bucket}?cors - DeleteBucketCors.
func (h *Handler) DeleteBucketCors(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	err := h.storage.DeleteBucketCors(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		WriteErrorWithResource(w, ErrInternalError, "/"+bucket)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleCorsPreflightRequest handles OPTIONS preflight requests.
func (h *Handler) HandleCorsPreflightRequest(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	origin := r.Header.Get("Origin")
	requestMethod := r.Header.Get("Access-Control-Request-Method")
	requestHeaders := r.Header.Get("Access-Control-Request-Headers")

	if origin == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Get CORS configuration
	cors, err := h.storage.GetBucketCors(r.Context(), bucket)
	if err != nil {
		// If no CORS config or bucket not found, return 200 without CORS headers
		w.WriteHeader(http.StatusOK)
		return
	}

	// Find matching CORS rule
	for _, rule := range cors.Rules {
		if matchOrigin(origin, rule.AllowedOrigins) && matchMethod(requestMethod, rule.AllowedMethods) {
			// Check if requested headers are allowed
			if requestHeaders != "" && !matchHeaders(requestHeaders, rule.AllowedHeaders) {
				continue
			}

			// Set CORS headers
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", strings.Join(rule.AllowedMethods, ", "))
			if len(rule.AllowedHeaders) > 0 {
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(rule.AllowedHeaders, ", "))
			}
			if len(rule.ExposeHeaders) > 0 {
				w.Header().Set("Access-Control-Expose-Headers", strings.Join(rule.ExposeHeaders, ", "))
			}
			if rule.MaxAgeSeconds > 0 {
				w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(rule.MaxAgeSeconds)))
			}
			w.Header().Set("Vary", "Origin, Access-Control-Request-Headers, Access-Control-Request-Method")
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	// No matching rule found
	w.WriteHeader(http.StatusOK)
}

// matchOrigin checks if the origin matches any of the allowed origins.
func matchOrigin(origin string, allowedOrigins []string) bool {
	// Parse origin to extract host
	parsedOrigin, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := parsedOrigin.Host

	for _, allowed := range allowedOrigins {
		if allowed == "*" || allowed == origin {
			return true
		}
		// Wildcard matching for *.example.com patterns
		// This should match "sub.example.com" but not "sub.example.com.evil.com"
		if strings.HasPrefix(allowed, "*.") {
			// Extract the domain part (e.g., "example.com" from "*.example.com")
			allowedDomain := strings.TrimPrefix(allowed, "*.")
			// Check if the origin host ends with the allowed domain
			// and has a dot before it (to ensure it's a subdomain, not a suffix attack)
			if strings.HasSuffix(originHost, "."+allowedDomain) || originHost == allowedDomain {
				return true
			}
		}
	}
	return false
}

// matchMethod checks if the method matches any of the allowed methods.
func matchMethod(method string, allowedMethods []string) bool {
	for _, allowed := range allowedMethods {
		if strings.EqualFold(allowed, method) {
			return true
		}
	}
	return false
}

// matchHeaders checks if all requested headers are allowed.
func matchHeaders(requestHeaders string, allowedHeaders []string) bool {
	// Check for wildcard
	for _, allowed := range allowedHeaders {
		if allowed == "*" {
			return true
		}
	}

	// Parse requested headers
	requested := strings.Split(requestHeaders, ",")
	for _, h := range requested {
		h = strings.TrimSpace(strings.ToLower(h))
		found := false
		for _, allowed := range allowedHeaders {
			if strings.EqualFold(allowed, h) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
