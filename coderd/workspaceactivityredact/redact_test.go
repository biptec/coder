package workspaceactivityredact

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvironment(t *testing.T) {
	t.Parallel()

	input := map[string]string{
		"SAFE_FLAG":    "visible",
		"API_TOKEN":    "secret-token",
		"DATABASE_URL": "postgres://user:password@example.test/db",
		"EMPTY_SECRET": "",
	}

	got := Environment(input)
	require.Equal(t, "visible", got["SAFE_FLAG"])
	require.Equal(t, RedactedValue, got["API_TOKEN"])
	require.Equal(t, RedactedValue, got["DATABASE_URL"])
	require.Equal(t, "", got["EMPTY_SECRET"])
	require.Equal(t, "secret-token", input["API_TOKEN"], "redaction must not mutate the caller map")
}

func TestTextPreservesFullContentAndRepairsInvalidUTF8(t *testing.T) {
	t.Parallel()

	large := strings.Repeat("activity-output-", 10_000)
	require.Equal(t, large, Text(large))
	require.Equal(t, "before�after", Text("before\xffafter"))
	require.Equal(t, "before�after", Text("before\x00after"))
}

func TestCommandArgvRedactsWholeSecretArguments(t *testing.T) {
	t.Parallel()

	got := CommandArgv([]string{"tool", "--token", "secret with spaces", "--verbose", "TOKEN=another secret"})
	require.Equal(t, "tool --token ***REDACTED*** --verbose TOKEN=***REDACTED***", got)
	require.NotContains(t, got, "secret with spaces")
	require.NotContains(t, got, "another secret")

	got = CommandArgv([]string{"psql", "postgres://user:password@example.test/db"})
	require.Equal(t, "psql postgres://user:***REDACTED***@example.test/db", got)
}

func TestCommandRedactsCommonSecretForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"flag-space", `tool --token supersecret --verbose`, `tool --token ***REDACTED*** --verbose`},
		{"flag-equals", `tool --api-key=supersecret run`, `tool --api-key=***REDACTED*** run`},
		{"flag-double-quoted", `tool --token="secret with spaces" run`, `tool --token="***REDACTED***" run`},
		{"flag-single-quoted", `tool --password 'secret with spaces' run`, `tool --password '***REDACTED***' run`},
		{"env-style", `TOKEN=supersecret tool run`, `TOKEN=***REDACTED*** tool run`},
		{"env-double-quoted", `API_TOKEN="secret with spaces" tool run`, `API_TOKEN="***REDACTED***" tool run`},
		{"bearer", `curl -H "Authorization: Bearer supersecret" x`, `curl -H "Authorization: Bearer ***REDACTED***" x`},
		{"basic", `curl -H "Authorization: Basic dXNlcjpwYXNz" x`, `curl -H "Authorization: Basic ***REDACTED***" x`},
		{"credential-url", `psql postgres://user:password@example.test/db`, `psql postgres://user:***REDACTED***@example.test/db`},
		{"safe", `go test ./...`, `go test ./...`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, Command(tt.input))
			require.NotContains(t, Command(tt.input), "supersecret")
		})
	}
}
