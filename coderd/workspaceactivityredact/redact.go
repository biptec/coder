package workspaceactivityredact

import (
	"net/url"
	"strings"
)

const RedactedValue = "***REDACTED***"

var sensitiveEnvironmentFragments = []string{
	"API_KEY",
	"APIKEY",
	"AUTHORIZATION",
	"CREDENTIAL",
	"PASSWORD",
	"PASSWD",
	"PRIVATE_KEY",
	"SECRET",
	"SESSION_TOKEN",
	"TOKEN",
	"ACCESS_KEY",
	"COOKIE",
}

// Environment returns a detached copy that is safe to persist in workspace
// activity history. Only explicitly supplied command environment overrides are
// passed here; inherited process environment is never recorded.
func Environment(environment map[string]string) map[string]string {
	if len(environment) == 0 {
		return map[string]string{}
	}
	redacted := make(map[string]string, len(environment))
	for key, value := range environment {
		if sensitiveEnvironmentKey(key) || credentialURL(value) {
			if value == "" {
				redacted[key] = ""
			} else {
				redacted[key] = RedactedValue
			}
			continue
		}
		redacted[key] = value
	}
	return redacted
}

func sensitiveEnvironmentKey(key string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(key))
	for _, fragment := range sensitiveEnvironmentFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

// Text normalizes arbitrary process/tool text for PostgreSQL without applying
// a storage-size limit. Activity History truncation is a presentation concern;
// the persisted value remains complete.
func Text(value string) string {
	// PostgreSQL text values cannot contain NUL bytes. NUL is valid UTF-8, so
	// strings.ToValidUTF8 alone does not make arbitrary process output safe to
	// persist. Replace it with the same replacement rune used for malformed
	// UTF-8 so command activity reporting cannot be poisoned by binary output.
	return strings.ReplaceAll(strings.ToValidUTF8(value, "\uFFFD"), "\x00", "\uFFFD")
}

func credentialURL(value string) bool {
	if !strings.Contains(value, "://") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return false
	}
	_, hasPassword := parsed.User.Password()
	return hasPassword
}
