package mcpclient

import (
	"testing"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestPlaywrightScreenshotToolForcesInlineMedia(t *testing.T) {
	t.Parallel()

	tool := mcp.Tool{
		Name:        "browser_take_screenshot",
		Description: "Take a screenshot",
		InputSchema: mcp.ToolInputSchema{
			Properties: map[string]any{
				"filename": map[string]any{"type": "string"},
				"type":     map[string]any{"type": "string"},
			},
			Required: []string{"filename", "type"},
		},
	}

	wrapped := newMCPTool(uuid.New(), "playwright", tool, nil, false)
	info := wrapped.Info()

	require.True(t, wrapped.forceInlineScreenshot)
	require.NotContains(t, info.Parameters, "filename")
	require.NotContains(t, info.Required, "filename")
	require.Contains(t, info.Description, "returned inline")
	// The source schema must remain untouched.
	require.Contains(t, tool.InputSchema.Properties, "filename")
}

func TestNonPlaywrightScreenshotKeepsFilename(t *testing.T) {
	t.Parallel()

	tool := mcp.Tool{
		Name: "browser_take_screenshot",
		InputSchema: mcp.ToolInputSchema{
			Properties: map[string]any{"filename": map[string]any{"type": "string"}},
		},
	}
	wrapped := newMCPTool(uuid.New(), "other", tool, nil, false)

	require.False(t, wrapped.forceInlineScreenshot)
	require.Contains(t, wrapped.Info().Parameters, "filename")
}
