package chatprompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeToolCallIDPreservesGeminiThoughtSignatureLosslessly(t *testing.T) {
	t.Parallel()

	const signature = "AbC+Def/Ghi=="
	got := sanitizeToolCallID("call+/id__thought__" + signature)

	require.NotContains(t, got, "+")
	require.NotContains(t, got, "/")
	require.Equal(t, "call__id", strings.SplitN(got, geminiThoughtSignatureMarker, 2)[0])

	suffix := strings.SplitN(got, geminiThoughtSignatureMarker, 2)[1]
	require.True(t, strings.HasPrefix(suffix, encodedGeminiThoughtSignaturePrefix))
	require.Contains(t, suffix, "_p")
	require.Contains(t, suffix, "_s")
	require.Contains(t, suffix, "_e")
	require.Less(t, len(got), len("call+/id"+geminiThoughtSignatureMarker+signature)+16)

	// Prompt normalization can run more than once; keep the safe form stable.
	require.Equal(t, got, sanitizeToolCallID(got))
}

func TestSanitizeToolCallIDUnchangedForOrdinarySafeID(t *testing.T) {
	t.Parallel()
	require.Equal(t, "call_123-safe", sanitizeToolCallID("call_123-safe"))
}
