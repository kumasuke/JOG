package api

import (
	"encoding/xml"
	"errors"
	"net/http"
	"strings"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// WebsiteConfigurationXML represents the XML format for website configuration.
type WebsiteConfigurationXML struct {
	XMLName               xml.Name                  `xml:"WebsiteConfiguration"`
	Xmlns                 string                    `xml:"xmlns,attr,omitempty"`
	IndexDocument         *IndexDocumentXML         `xml:"IndexDocument,omitempty"`
	ErrorDocument         *ErrorDocumentXML         `xml:"ErrorDocument,omitempty"`
	RedirectAllRequestsTo *RedirectAllRequestsToXML `xml:"RedirectAllRequestsTo,omitempty"`
	RoutingRules          *RoutingRulesXML          `xml:"RoutingRules,omitempty"`
}

// IndexDocumentXML represents the index document in XML.
type IndexDocumentXML struct {
	Suffix string `xml:"Suffix"`
}

// ErrorDocumentXML represents the error document in XML.
type ErrorDocumentXML struct {
	Key string `xml:"Key"`
}

// RedirectAllRequestsToXML represents redirect all requests configuration in XML.
type RedirectAllRequestsToXML struct {
	HostName string `xml:"HostName"`
	Protocol string `xml:"Protocol,omitempty"`
}

// RoutingRulesXML represents routing rules in XML.
type RoutingRulesXML struct {
	RoutingRule []RoutingRuleXML `xml:"RoutingRule"`
}

// RoutingRuleXML represents a single routing rule in XML.
type RoutingRuleXML struct {
	Condition *ConditionXML `xml:"Condition,omitempty"`
	Redirect  *RedirectXML  `xml:"Redirect"`
}

// ConditionXML represents a routing rule condition in XML.
type ConditionXML struct {
	KeyPrefixEquals             string `xml:"KeyPrefixEquals,omitempty"`
	HttpErrorCodeReturnedEquals string `xml:"HttpErrorCodeReturnedEquals,omitempty"`
}

// RedirectXML represents redirect configuration in XML.
type RedirectXML struct {
	HostName             string `xml:"HostName,omitempty"`
	HttpRedirectCode     string `xml:"HttpRedirectCode,omitempty"`
	Protocol             string `xml:"Protocol,omitempty"`
	ReplaceKeyPrefixWith string `xml:"ReplaceKeyPrefixWith,omitempty"`
	ReplaceKeyWith       string `xml:"ReplaceKeyWith,omitempty"`
}

// PutBucketWebsite handles PUT /{bucket}?website - PutBucketWebsite.
func (h *Handler) PutBucketWebsite(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)
	limitBody(w, r, MaxWebsiteBodySize)

	var xmlConfig WebsiteConfigurationXML
	if err := xml.NewDecoder(r.Body).Decode(&xmlConfig); err != nil {
		if isBodyTooLarge(err) {
			WriteErrorWithResource(w, ErrEntityTooLarge, "/"+bucket)
			return
		}
		WriteError(w, ErrMalformedXML)
		return
	}

	// Validate website configuration
	if err := validateWebsiteConfig(&xmlConfig); err != nil {
		WriteError(w, err)
		return
	}

	// Convert XML to storage type
	config := xmlToStorageWebsiteConfig(&xmlConfig)

	err := h.storage.PutBucketWebsite(r.Context(), bucket, config)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		log.Error().Err(err).Str("bucket", bucket).Msg("Failed to put bucket website")
		WriteError(w, ErrInternalError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GetBucketWebsite handles GET /{bucket}?website - GetBucketWebsite.
func (h *Handler) GetBucketWebsite(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	config, err := h.storage.GetBucketWebsite(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		if errors.Is(err, storage.ErrNoSuchWebsiteConfiguration) {
			WriteErrorWithResource(w, ErrNoSuchWebsiteConfiguration, "/"+bucket)
			return
		}
		log.Error().Err(err).Str("bucket", bucket).Msg("Failed to get bucket website")
		WriteError(w, ErrInternalError)
		return
	}

	// Convert storage type to XML
	xmlConfig := storageToXMLWebsiteConfig(config)
	xmlConfig.Xmlns = "http://s3.amazonaws.com/doc/2006-03-01/"

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	if err := xml.NewEncoder(w).Encode(xmlConfig); err != nil {
		log.Error().Err(err).Msg("Failed to encode GetBucketWebsite response")
	}
}

// DeleteBucketWebsite handles DELETE /{bucket}?website - DeleteBucketWebsite.
func (h *Handler) DeleteBucketWebsite(w http.ResponseWriter, r *http.Request) {
	bucket := GetBucket(r)

	err := h.storage.DeleteBucketWebsite(r.Context(), bucket)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotFound) {
			WriteErrorWithResource(w, ErrNoSuchBucket, "/"+bucket)
			return
		}
		log.Error().Err(err).Str("bucket", bucket).Msg("Failed to delete bucket website")
		WriteError(w, ErrInternalError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// xmlToStorageWebsiteConfig converts XML website config to storage type.
func xmlToStorageWebsiteConfig(xmlConfig *WebsiteConfigurationXML) *storage.WebsiteConfiguration {
	config := &storage.WebsiteConfiguration{}

	if xmlConfig.IndexDocument != nil {
		config.IndexDocument = &storage.IndexDocument{
			Suffix: xmlConfig.IndexDocument.Suffix,
		}
	}

	if xmlConfig.ErrorDocument != nil {
		config.ErrorDocument = &storage.ErrorDocument{
			Key: xmlConfig.ErrorDocument.Key,
		}
	}

	if xmlConfig.RedirectAllRequestsTo != nil {
		config.RedirectAllRequestsTo = &storage.RedirectAllRequestsTo{
			HostName: xmlConfig.RedirectAllRequestsTo.HostName,
			Protocol: xmlConfig.RedirectAllRequestsTo.Protocol,
		}
	}

	if xmlConfig.RoutingRules != nil {
		for _, rule := range xmlConfig.RoutingRules.RoutingRule {
			storageRule := storage.RoutingRule{}

			if rule.Condition != nil {
				storageRule.Condition = &storage.Condition{
					KeyPrefixEquals:             rule.Condition.KeyPrefixEquals,
					HttpErrorCodeReturnedEquals: rule.Condition.HttpErrorCodeReturnedEquals,
				}
			}

			if rule.Redirect != nil {
				storageRule.Redirect = &storage.Redirect{
					HostName:             rule.Redirect.HostName,
					HttpRedirectCode:     rule.Redirect.HttpRedirectCode,
					Protocol:             rule.Redirect.Protocol,
					ReplaceKeyPrefixWith: rule.Redirect.ReplaceKeyPrefixWith,
					ReplaceKeyWith:       rule.Redirect.ReplaceKeyWith,
				}
			}

			config.RoutingRules = append(config.RoutingRules, storageRule)
		}
	}

	return config
}

// storageToXMLWebsiteConfig converts storage website config to XML type.
func storageToXMLWebsiteConfig(config *storage.WebsiteConfiguration) *WebsiteConfigurationXML {
	result := &WebsiteConfigurationXML{}

	if config.IndexDocument != nil {
		result.IndexDocument = &IndexDocumentXML{
			Suffix: config.IndexDocument.Suffix,
		}
	}

	if config.ErrorDocument != nil {
		result.ErrorDocument = &ErrorDocumentXML{
			Key: config.ErrorDocument.Key,
		}
	}

	if config.RedirectAllRequestsTo != nil {
		result.RedirectAllRequestsTo = &RedirectAllRequestsToXML{
			HostName: config.RedirectAllRequestsTo.HostName,
			Protocol: config.RedirectAllRequestsTo.Protocol,
		}
	}

	if len(config.RoutingRules) > 0 {
		result.RoutingRules = &RoutingRulesXML{}
		for _, rule := range config.RoutingRules {
			xmlRule := RoutingRuleXML{}

			if rule.Condition != nil {
				xmlRule.Condition = &ConditionXML{
					KeyPrefixEquals:             rule.Condition.KeyPrefixEquals,
					HttpErrorCodeReturnedEquals: rule.Condition.HttpErrorCodeReturnedEquals,
				}
			}

			if rule.Redirect != nil {
				xmlRule.Redirect = &RedirectXML{
					HostName:             rule.Redirect.HostName,
					HttpRedirectCode:     rule.Redirect.HttpRedirectCode,
					Protocol:             rule.Redirect.Protocol,
					ReplaceKeyPrefixWith: rule.Redirect.ReplaceKeyPrefixWith,
					ReplaceKeyWith:       rule.Redirect.ReplaceKeyWith,
				}
			}

			result.RoutingRules.RoutingRule = append(result.RoutingRules.RoutingRule, xmlRule)
		}
	}

	return result
}

// validateWebsiteConfig validates the website configuration according to S3 rules.
func validateWebsiteConfig(config *WebsiteConfigurationXML) *S3Error {
	hasRedirectAll := config.RedirectAllRequestsTo != nil
	hasIndexDoc := config.IndexDocument != nil
	hasErrorDoc := config.ErrorDocument != nil
	hasRoutingRules := config.RoutingRules != nil && len(config.RoutingRules.RoutingRule) > 0

	// RedirectAllRequestsTo is mutually exclusive with other options
	if hasRedirectAll && (hasIndexDoc || hasErrorDoc || hasRoutingRules) {
		return ErrInvalidRequest
	}

	// If not using RedirectAllRequestsTo, IndexDocument is required
	if !hasRedirectAll && !hasIndexDoc {
		return ErrInvalidRequest
	}

	// IndexDocument must have a non-empty Suffix
	if hasIndexDoc && config.IndexDocument.Suffix == "" {
		return ErrInvalidRequest
	}

	// RedirectAllRequestsTo must have a non-empty HostName
	if hasRedirectAll {
		if config.RedirectAllRequestsTo.HostName == "" {
			return ErrInvalidRequest
		}
		if !isValidRedirectHostName(config.RedirectAllRequestsTo.HostName) {
			return ErrInvalidRequest
		}
		if !isValidRedirectProtocol(config.RedirectAllRequestsTo.Protocol) {
			return ErrInvalidRequest
		}
	}

	// Validate routing rules
	if hasRoutingRules {
		for _, rule := range config.RoutingRules.RoutingRule {
			// Each routing rule must have a Redirect
			if rule.Redirect == nil {
				return ErrInvalidRequest
			}
			if rule.Redirect.HostName != "" && !isValidRedirectHostName(rule.Redirect.HostName) {
				return ErrInvalidRequest
			}
			if !isValidRedirectProtocol(rule.Redirect.Protocol) {
				return ErrInvalidRequest
			}
			if !isValidHTTPRedirectCode(rule.Redirect.HttpRedirectCode) {
				return ErrInvalidRequest
			}
		}
	}

	return nil
}

// isValidRedirectProtocol reports whether p is an acceptable Protocol value
// for a website redirect. Empty (use the request's protocol) is allowed;
// otherwise only "http" or "https" are accepted. Why: this value is
// concatenated into the Location response header, so allowing "javascript"
// or other URI schemes here would enable reflected XSS (H-12).
func isValidRedirectProtocol(p string) bool {
	switch p {
	case "", "http", "https":
		return true
	default:
		return false
	}
}

// isValidRedirectHostName reports whether host is a syntactically plausible
// hostname for a redirect target. It rejects values that contain characters
// which would let an attacker break out of the Host portion of the Location
// URL: CR/LF (HTTP response splitting), '/' '?' '#' '@' ':' (path/query/
// userinfo/scheme injection), and whitespace. Why: HostName is concatenated
// into the redirect URL with minimal escaping (H-12).
func isValidRedirectHostName(host string) bool {
	if host == "" {
		return false
	}
	if strings.ContainsAny(host, "\r\n\t /?#@: \\") {
		return false
	}
	return true
}

// isValidHTTPRedirectCode reports whether code (the string form sent in the
// XML) is one of the 3xx values S3 documents as supported. Empty is allowed
// because the field is optional and defaults to 301. Why: feeding an
// arbitrary status to w.WriteHeader breaks the redirect contract and is
// an avenue for response manipulation (H-12).
func isValidHTTPRedirectCode(code string) bool {
	switch code {
	case "", "301", "302", "303", "307", "308":
		return true
	default:
		return false
	}
}
