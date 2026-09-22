package workspaceactivityredact

import (
	"net/url"
	"regexp"
	"strings"
)

const RedactedValue = "***REDACTED***"

var commandSecretPatterns = []*regexp.Regexp{
	// Quoted forms come first so values containing whitespace are redacted as
	// one unit instead of leaking the tail after the first word.
	regexp.MustCompile(`(?i)(--(?:password|passwd|token|api[-_]?key|apikey|access[-_]?key|secret|authorization)(?:=|\s+)")([^"]*)(")`),
	regexp.MustCompile(`(?i)(--(?:password|passwd|token|api[-_]?key|apikey|access[-_]?key|secret|authorization)(?:=|\s+)')([^']*)(')`),
	regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API_KEY|APIKEY|ACCESS_KEY)[A-Z0-9_]*=")([^"]*)(")`),
	regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API_KEY|APIKEY|ACCESS_KEY)[A-Z0-9_]*=')([^']*)(')`),
	regexp.MustCompile(`(?i)(--(?:password|passwd|token|api[-_]?key|apikey|access[-_]?key|secret|authorization)(?:=|\s+))([^\s"']+)`),
	regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(?:TOKEN|SECRET|PASSWORD|PASSWD|API_KEY|APIKEY|ACCESS_KEY)[A-Z0-9_]*=)([^\s"']+)`),
	regexp.MustCompile(`(?i)(\b(?:Authorization:)?\s*Bearer\s+)([^\s"']+)`),
	regexp.MustCompile(`(?i)(Authorization:\s*Basic\s+)([^\s"']+)`),
	regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+:)([^\s@/]+)(@)`),
}

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

// Command returns a model-safe display form of a process command. It preserves
// useful execution context while redacting common secret-bearing CLI forms.
// This is intentionally a presentation guard, not a claim that arbitrary argv
// can be perfectly classified as secret or non-secret.
func Command(value string) string {
	redacted := Text(value)
	for _, pattern := range commandSecretPatterns {
		redacted = pattern.ReplaceAllString(redacted, `${1}`+RedactedValue+`${3}`)
	}
	return redacted
}

var sensitiveCLIFlags = map[string]struct{}{
	"--password":      {},
	"--passwd":        {},
	"--token":         {},
	"--api-key":       {},
	"--api_key":       {},
	"--apikey":        {},
	"--access-key":    {},
	"--access_key":    {},
	"--secret":        {},
	"--authorization": {},
}

// CommandArgv redacts structured argv before joining it for model-facing
// display. Unlike regex-only redaction, argument boundaries preserve secret
// values that themselves contain whitespace.
func CommandArgv(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	display := make([]string, len(argv))
	redactNext := false
	for i, raw := range argv {
		arg := Text(raw)
		if redactNext {
			display[i] = RedactedValue
			redactNext = false
			continue
		}

		if _, ok := sensitiveCLIFlags[strings.ToLower(arg)]; ok {
			display[i] = arg
			redactNext = true
			continue
		}

		if equals := strings.IndexByte(arg, '='); equals > 0 {
			name := arg[:equals]
			if _, ok := sensitiveCLIFlags[strings.ToLower(name)]; ok || sensitiveEnvironmentKey(name) {
				display[i] = name + "=" + RedactedValue
				continue
			}
		}

		display[i] = Command(arg)
	}
	return strings.Join(display, " ")
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
