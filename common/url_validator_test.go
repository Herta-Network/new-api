package common

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateRedirectURL(t *testing.T) {
	// Save original trusted domains and restore after test
	originalDomains := constant.TrustedRedirectDomains
	defer func() {
		constant.TrustedRedirectDomains = originalDomains
	}()

	tests := []struct {
		name           string
		url            string
		trustedDomains []string
		wantErr        bool
		errContains    string
	}{
		// Valid cases
		{
			name:           "exact domain match with https",
			url:            "https://example.com/success",
			trustedDomains: []string{"example.com"},
			wantErr:        false,
		},
		{
			name:           "exact domain match with http",
			url:            "http://example.com/callback",
			trustedDomains: []string{"example.com"},
			wantErr:        false,
		},
		{
			name:           "subdomain match",
			url:            "https://sub.example.com/success",
			trustedDomains: []string{"example.com"},
			wantErr:        false,
		},
		{
			name:           "case insensitive domain",
			url:            "https://EXAMPLE.COM/success",
			trustedDomains: []string{"example.com"},
			wantErr:        false,
		},

		// Invalid cases - untrusted domain
		{
			name:           "untrusted domain",
			url:            "https://evil.com/phishing",
			trustedDomains: []string{"example.com"},
			wantErr:        true,
			errContains:    "not in the trusted domains list",
		},
		{
			name:           "suffix attack - fakeexample.com",
			url:            "https://fakeexample.com/success",
			trustedDomains: []string{"example.com"},
			wantErr:        true,
			errContains:    "not in the trusted domains list",
		},
		{
			name:           "empty trusted domains list",
			url:            "https://example.com/success",
			trustedDomains: []string{},
			wantErr:        true,
			errContains:    "not in the trusted domains list",
		},

		// Invalid cases - scheme
		{
			name:           "javascript scheme",
			url:            "javascript:alert('xss')",
			trustedDomains: []string{"example.com"},
			wantErr:        true,
			errContains:    "invalid URL scheme",
		},
		{
			name:           "data scheme",
			url:            "data:text/html,<script>alert('xss')</script>",
			trustedDomains: []string{"example.com"},
			wantErr:        true,
			errContains:    "invalid URL scheme",
		},

		// Edge cases
		{
			name:           "empty URL",
			url:            "",
			trustedDomains: []string{"example.com"},
			wantErr:        true,
			errContains:    "invalid URL scheme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up trusted domains for this test case
			constant.TrustedRedirectDomains = tt.trustedDomains

			err := ValidateRedirectURL(tt.url)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateRedirectURL(%q) expected error containing %q, got nil", tt.url, tt.errContains)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateRedirectURL(%q) error = %q, want error containing %q", tt.url, err.Error(), tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateRedirectURL(%q) unexpected error: %v", tt.url, err)
				}
			}
		})
	}
}

func useFrontendOriginEnv(t *testing.T, frontendOrigins, legacyTrustedURL string) {
	t.Helper()
	t.Setenv("FRONTEND_ORIGINS", frontendOrigins)
	t.Setenv("SESSION_COOKIE_TRUSTED_URL", legacyTrustedURL)
	t.Setenv("SESSION_COOKIE_SECURE", "")
	t.Cleanup(func() { FrontendOrigins = nil })
}

func TestInitFrontendOriginsDefaultsToNone(t *testing.T) {
	useFrontendOriginEnv(t, "", "")
	require.NoError(t, InitFrontendOrigins())
	assert.Empty(t, FrontendOrigins)
}

func TestInitFrontendOriginsMergesTheDeprecatedTrustedURL(t *testing.T) {
	useFrontendOriginEnv(t, "https://APP.example.com:443, http://localhost:5173/", "https://app.example.com,https://admin.example.com:8443")
	require.NoError(t, InitFrontendOrigins())
	assert.Equal(t, []string{"https://app.example.com", "http://localhost:5173", "https://admin.example.com:8443"}, FrontendOrigins)
}

func TestInitFrontendOriginsRejectsNonOriginEntries(t *testing.T) {
	for _, value := range []string{
		"https://example.com,",
		"https://*.example.com",
		"https://user@example.com",
		"https://example.com/admin",
		"https://example.com?next=admin",
		"https://example.com#admin",
		"ftp://example.com",
		"null",
	} {
		t.Run(value, func(t *testing.T) {
			useFrontendOriginEnv(t, value, "")
			require.Error(t, InitFrontendOrigins())
		})
	}
	useFrontendOriginEnv(t, "", "https://example.com/admin")
	require.Error(t, InitFrontendOrigins(), "the deprecated variable is validated the same way")
}
