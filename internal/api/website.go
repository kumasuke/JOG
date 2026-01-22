package api

import (
	"encoding/xml"
	"errors"
	"net/http"

	"github.com/kumasuke/jog/internal/storage"
	"github.com/rs/zerolog/log"
)

// WebsiteConfigurationXML represents the XML format for website configuration.
type WebsiteConfigurationXML struct {
	XMLName               xml.Name                     `xml:"WebsiteConfiguration"`
	Xmlns                 string                       `xml:"xmlns,attr,omitempty"`
	IndexDocument         *IndexDocumentXML            `xml:"IndexDocument,omitempty"`
	ErrorDocument         *ErrorDocumentXML            `xml:"ErrorDocument,omitempty"`
	RedirectAllRequestsTo *RedirectAllRequestsToXML    `xml:"RedirectAllRequestsTo,omitempty"`
	RoutingRules          *RoutingRulesXML             `xml:"RoutingRules,omitempty"`
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

	var xmlConfig WebsiteConfigurationXML
	if err := xml.NewDecoder(r.Body).Decode(&xmlConfig); err != nil {
		WriteError(w, ErrMalformedXML)
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
func xmlToStorageWebsiteConfig(xml *WebsiteConfigurationXML) *storage.WebsiteConfiguration {
	config := &storage.WebsiteConfiguration{}

	if xml.IndexDocument != nil {
		config.IndexDocument = &storage.IndexDocument{
			Suffix: xml.IndexDocument.Suffix,
		}
	}

	if xml.ErrorDocument != nil {
		config.ErrorDocument = &storage.ErrorDocument{
			Key: xml.ErrorDocument.Key,
		}
	}

	if xml.RedirectAllRequestsTo != nil {
		config.RedirectAllRequestsTo = &storage.RedirectAllRequestsTo{
			HostName: xml.RedirectAllRequestsTo.HostName,
			Protocol: xml.RedirectAllRequestsTo.Protocol,
		}
	}

	if xml.RoutingRules != nil {
		for _, rule := range xml.RoutingRules.RoutingRule {
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
	xml := &WebsiteConfigurationXML{}

	if config.IndexDocument != nil {
		xml.IndexDocument = &IndexDocumentXML{
			Suffix: config.IndexDocument.Suffix,
		}
	}

	if config.ErrorDocument != nil {
		xml.ErrorDocument = &ErrorDocumentXML{
			Key: config.ErrorDocument.Key,
		}
	}

	if config.RedirectAllRequestsTo != nil {
		xml.RedirectAllRequestsTo = &RedirectAllRequestsToXML{
			HostName: config.RedirectAllRequestsTo.HostName,
			Protocol: config.RedirectAllRequestsTo.Protocol,
		}
	}

	if len(config.RoutingRules) > 0 {
		xml.RoutingRules = &RoutingRulesXML{}
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

			xml.RoutingRules.RoutingRule = append(xml.RoutingRules.RoutingRule, xmlRule)
		}
	}

	return xml
}
