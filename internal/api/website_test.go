package api

import "testing"

// TestValidateWebsiteConfig_RejectsJavascriptProtocol verifies that a website
// configuration declaring Protocol="javascript" (or any scheme other than
// http/https) is rejected (H-12). Without this check a redirect would render
// a "javascript:" URL into the Location header, enabling reflected XSS via
// the website endpoint.
func TestValidateWebsiteConfig_RejectsJavascriptProtocol(t *testing.T) {
	config := &WebsiteConfigurationXML{
		RedirectAllRequestsTo: &RedirectAllRequestsToXML{
			HostName: "example.com",
			Protocol: "javascript",
		},
	}
	if err := validateWebsiteConfig(config); err == nil {
		t.Fatal("validateWebsiteConfig() returned nil for Protocol=javascript; want rejection")
	}
}

// TestValidateWebsiteConfig_RejectsHostNameWithCRLF verifies that a HostName
// containing CR/LF is rejected (H-12). A raw newline in HostName would land
// in the Location response header and enable HTTP response splitting.
func TestValidateWebsiteConfig_RejectsHostNameWithCRLF(t *testing.T) {
	config := &WebsiteConfigurationXML{
		RedirectAllRequestsTo: &RedirectAllRequestsToXML{
			HostName: "example.com\r\nX-Injected: yes",
		},
	}
	if err := validateWebsiteConfig(config); err == nil {
		t.Fatal("validateWebsiteConfig() returned nil for HostName with CRLF; want rejection")
	}
}

// TestValidateWebsiteConfig_RejectsHostNameWithSlash verifies that a HostName
// containing a slash, query, or scheme is rejected (H-12). HostName must be
// a bare host so the redirect cannot escape the configured target.
func TestValidateWebsiteConfig_RejectsHostNameWithSlash(t *testing.T) {
	cases := []string{
		"attacker.com/path",
		"attacker.com?evil=1",
		"https://attacker.com",
		"attacker.com#frag",
		"attacker.com user@",
	}
	for _, host := range cases {
		t.Run(host, func(t *testing.T) {
			config := &WebsiteConfigurationXML{
				RedirectAllRequestsTo: &RedirectAllRequestsToXML{
					HostName: host,
				},
			}
			if err := validateWebsiteConfig(config); err == nil {
				t.Fatalf("validateWebsiteConfig() returned nil for HostName=%q; want rejection", host)
			}
		})
	}
}

// TestValidateWebsiteConfig_RejectsInvalidRedirectCode verifies that
// HttpRedirectCode must be one of the supported 3xx values (H-12).
func TestValidateWebsiteConfig_RejectsInvalidRedirectCode(t *testing.T) {
	cases := []string{"200", "404", "500", "abc", "3xx", "999"}
	for _, code := range cases {
		t.Run(code, func(t *testing.T) {
			config := &WebsiteConfigurationXML{
				IndexDocument: &IndexDocumentXML{Suffix: "index.html"},
				RoutingRules: &RoutingRulesXML{
					RoutingRule: []RoutingRuleXML{
						{Redirect: &RedirectXML{HttpRedirectCode: code, HostName: "example.com"}},
					},
				},
			}
			if err := validateWebsiteConfig(config); err == nil {
				t.Fatalf("validateWebsiteConfig() returned nil for HttpRedirectCode=%q; want rejection", code)
			}
		})
	}
}

// TestValidateWebsiteConfig_AcceptsValidRedirect verifies that a fully valid
// redirect configuration passes validation.
func TestValidateWebsiteConfig_AcceptsValidRedirect(t *testing.T) {
	config := &WebsiteConfigurationXML{
		IndexDocument: &IndexDocumentXML{Suffix: "index.html"},
		RoutingRules: &RoutingRulesXML{
			RoutingRule: []RoutingRuleXML{
				{
					Redirect: &RedirectXML{
						HostName:         "example.com",
						Protocol:         "https",
						HttpRedirectCode: "301",
					},
				},
			},
		},
	}
	if err := validateWebsiteConfig(config); err != nil {
		t.Fatalf("validateWebsiteConfig() returned %v for valid config; want nil", err)
	}
}

// TestValidateWebsiteConfig_AcceptsEmptyOptionalFields confirms validation
// stays permissive when optional Protocol/HostName/HttpRedirectCode are
// omitted, so existing configs that only set ReplaceKeyPrefixWith continue
// to work.
func TestValidateWebsiteConfig_AcceptsEmptyOptionalFields(t *testing.T) {
	config := &WebsiteConfigurationXML{
		IndexDocument: &IndexDocumentXML{Suffix: "index.html"},
		RoutingRules: &RoutingRulesXML{
			RoutingRule: []RoutingRuleXML{
				{Redirect: &RedirectXML{ReplaceKeyPrefixWith: "errors/"}},
			},
		},
	}
	if err := validateWebsiteConfig(config); err != nil {
		t.Fatalf("validateWebsiteConfig() returned %v for minimal valid config; want nil", err)
	}
}
