package chatd

import (
	"context"
	"encoding/json"
	"testing"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
)

type identifiedScreenshotTool struct {
	info            fantasy.ToolInfo
	configID        uuid.UUID
	lastInput       string
	providerOptions fantasy.ProviderOptions
}

func (t *identifiedScreenshotTool) MCPServerConfigID() uuid.UUID { return t.configID }
func (t *identifiedScreenshotTool) Info() fantasy.ToolInfo       { return t.info }
func (t *identifiedScreenshotTool) ProviderOptions() fantasy.ProviderOptions {
	return t.providerOptions
}
func (t *identifiedScreenshotTool) SetProviderOptions(options fantasy.ProviderOptions) {
	t.providerOptions = options
}
func (t *identifiedScreenshotTool) Run(_ context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	t.lastInput = call.Input
	return fantasy.ToolResponse{
		Type:      "image",
		Content:   "Screenshot captured",
		Data:      []byte("png-data"),
		MediaType: "image/png",
	}, nil
}

func TestPlaywrightScreenshotAttachmentTool(t *testing.T) {
	t.Parallel()

	configID := uuid.New()
	base := &identifiedScreenshotTool{
		configID: configID,
		info: fantasy.ToolInfo{
			Name: playwrightInlineScreenshotToolName,
			Parameters: map[string]any{
				"fullPage": map[string]any{"type": "boolean"},
			},
		},
	}
	stored := false
	store := func(_ context.Context, name, detectName string, data []byte) (chattool.AttachmentMetadata, error) {
		stored = true
		require.Equal(t, "cnn-homepage.png", name)
		require.Equal(t, name, detectName)
		require.Equal(t, []byte("png-data"), data)
		return chattool.AttachmentMetadata{
			FileID:    uuid.New(),
			MediaType: "image/png",
			Name:      name,
		}, nil
	}

	tools := appendPlaywrightScreenshotAttachmentTool([]fantasy.AgentTool{base}, store)
	require.Len(t, tools, 2)
	saveTool := tools[1]
	require.Equal(t, playwrightSaveScreenshotToolName, saveTool.Info().Name)
	require.Contains(t, saveTool.Info().Required, "filename")
	require.Contains(t, saveTool.Info().Parameters, "filename")

	response, err := saveTool.Run(context.Background(), fantasy.ToolCall{
		ID:    "call-1",
		Input: `{"filename":"cnn-homepage.png","fullPage":true}`,
	})
	require.NoError(t, err)
	require.False(t, response.IsError)
	require.True(t, stored)
	require.Equal(t, []byte("png-data"), response.Data)
	require.Contains(t, response.Content, "cnn-homepage.png")
	require.NotEmpty(t, response.Metadata)

	var forwarded map[string]any
	require.NoError(t, json.Unmarshal([]byte(base.lastInput), &forwarded))
	require.NotContains(t, forwarded, "filename")
	require.Equal(t, true, forwarded["fullPage"])

	identified, ok := saveTool.(interface{ MCPServerConfigID() uuid.UUID })
	require.True(t, ok)
	require.Equal(t, configID, identified.MCPServerConfigID())
}
