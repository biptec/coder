package chatloop

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"charm.land/fantasy"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3"
)

func TestToolResultByteBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		contextLimit int64
		want         int
	}{
		{name: "Unknown", contextLimit: 0, want: defaultToolResultBytes},
		{name: "Negative", contextLimit: -1, want: defaultToolResultBytes},
		{name: "BelowFloor", contextLimit: 1000, want: minToolResultBytes},
		{
			name:         "LargeWindow",
			contextLimit: 200_000,
			want:         200_000 / toolResultContextDivisor * bytesPerTokenEstimate,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, toolResultByteBudget(tt.contextLimit))
		})
	}

	t.Run("NeverBelowFloor", func(t *testing.T) {
		t.Parallel()
		for limit := int64(1); limit <= 200_000; limit += 137 {
			assert.GreaterOrEqual(t, toolResultByteBudget(limit), minToolResultBytes)
		}
	})
}

func TestTruncateToolResultText(t *testing.T) {
	t.Parallel()

	t.Run("UnderLimitUnchanged", func(t *testing.T) {
		t.Parallel()
		in := "small output"
		out, truncated := truncateToolResultText(in, 1024)
		assert.False(t, truncated)
		assert.Equal(t, in, out)
	})

	t.Run("ExactlyAtLimitUnchanged", func(t *testing.T) {
		t.Parallel()
		in := strings.Repeat("a", 1024)
		out, truncated := truncateToolResultText(in, 1024)
		assert.False(t, truncated)
		assert.Equal(t, in, out)
	})

	t.Run("ZeroBudgetUnchanged", func(t *testing.T) {
		t.Parallel()
		in := strings.Repeat("a", 1024)
		out, truncated := truncateToolResultText(in, 0)
		assert.False(t, truncated)
		assert.Equal(t, in, out)
	})

	t.Run("PreservesHeadAndTail", func(t *testing.T) {
		t.Parallel()
		in := strings.Repeat("A", 1000) + "MIDDLE" + strings.Repeat("B", 1000)
		const maxBytes = 600
		out, truncated := truncateToolResultText(in, maxBytes)
		require.True(t, truncated)
		assert.LessOrEqual(t, len(out), maxBytes)
		assert.True(t, utf8.ValidString(out))
		assert.True(t, strings.HasPrefix(out, strings.Repeat("A", 100)))
		assert.True(t, strings.HasSuffix(out, strings.Repeat("B", 100)))
		assert.NotContains(t, out, "MIDDLE")
		assert.Contains(t, out, "truncated")
	})

	t.Run("MultibyteStaysValid", func(t *testing.T) {
		t.Parallel()
		// Each rune is 3 bytes, so cuts routinely land mid-rune.
		in := strings.Repeat("界", 1000)
		const maxBytes = 600
		out, truncated := truncateToolResultText(in, maxBytes)
		require.True(t, truncated)
		assert.LessOrEqual(t, len(out), maxBytes)
		assert.True(t, utf8.ValidString(out), "truncated output must be valid UTF-8")
	})

	t.Run("TinyBudgetHardCut", func(t *testing.T) {
		t.Parallel()
		in := strings.Repeat("界", 10) // 30 bytes
		const maxBytes = 10
		out, truncated := truncateToolResultText(in, maxBytes)
		require.True(t, truncated)
		assert.LessOrEqual(t, len(out), maxBytes)
		assert.True(t, utf8.ValidString(out))
	})
}

// exemptTool wraps a fantasy.AgentTool with a ResultTruncationExempter
// implementation, mirroring how the structured output finalizer opts
// out of result truncation.
type exemptTool struct {
	fantasy.AgentTool
}

func (exemptTool) ExemptFromResultTruncation() bool { return true }

func TestExecuteSingleToolResultTruncationExemption(t *testing.T) {
	t.Parallel()

	logger := slog.Make()
	// Comfortably larger than the truncation budget used below.
	bigPayload := strings.Repeat("x", minToolResultBytes*3)

	newTool := func(name string, isError bool) fantasy.AgentTool {
		return fantasy.NewAgentTool(
			name,
			"returns a large payload",
			func(_ context.Context, _ struct{}, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
				if isError {
					return fantasy.NewTextErrorResponse(bigPayload), nil
				}
				return fantasy.NewTextResponse(bigPayload), nil
			},
		)
	}

	runTool := func(t *testing.T, tool fantasy.AgentTool, name string) fantasy.ToolResultContent {
		t.Helper()
		return executeSingleTool(
			context.Background(),
			map[string]fantasy.AgentTool{name: tool},
			fantasy.ToolCallContent{ToolCallID: "call-1", ToolName: name, Input: "{}"},
			NewMetrics(prometheus.NewRegistry()),
			logger,
			"fake", "fake-model",
			map[string]bool{},
			[]string{name},
			map[string]struct{}{},
			nil,
			minToolResultBytes,
			nil,
		)
	}

	t.Run("ExemptSuccessNotTruncated", func(t *testing.T) {
		t.Parallel()
		result := runTool(t, exemptTool{newTool("finalize", false)}, "finalize")
		text, ok := result.Result.(fantasy.ToolResultOutputContentText)
		require.True(t, ok, "expected text result, got %T", result.Result)
		require.Equal(t, bigPayload, text.Text, "exempt successful result must not be truncated")
	})

	t.Run("ExemptErrorStillTruncated", func(t *testing.T) {
		t.Parallel()
		result := runTool(t, exemptTool{newTool("finalize", true)}, "finalize")
		errResult, ok := result.Result.(fantasy.ToolResultOutputContentError)
		require.True(t, ok, "expected error result, got %T", result.Result)
		require.Less(t, len(errResult.Error.Error()), len(bigPayload))
		require.Contains(t, errResult.Error.Error(), "truncated")
	})

	t.Run("NonExemptSuccessTruncated", func(t *testing.T) {
		t.Parallel()
		result := runTool(t, newTool("fetch", false), "fetch")
		text, ok := result.Result.(fantasy.ToolResultOutputContentText)
		require.True(t, ok, "expected text result, got %T", result.Result)
		require.Less(t, len(text.Text), len(bigPayload))
		require.Contains(t, text.Text, "truncated")
	})
}
