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
