package chatd

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"charm.land/fantasy"
	"github.com/google/uuid"

	"github.com/coder/coder/v2/coderd/x/chatd/chattool"
	"github.com/coder/coder/v2/coderd/x/chatd/mcpclient"
)

const (
	playwrightInlineScreenshotToolName = "playwright__browser_take_screenshot"
	playwrightSaveScreenshotToolName   = "playwright__browser_save_screenshot"
)

type playwrightScreenshotAttachmentTool struct {
	base     fantasy.AgentTool
	configID uuid.UUID
	store    chattool.StoreFileFunc
}

func appendPlaywrightScreenshotAttachmentTool(
	tools []fantasy.AgentTool,
	store chattool.StoreFileFunc,
) []fantasy.AgentTool {
	if store == nil {
		return tools
	}
	for _, tool := range tools {
		if tool.Info().Name != playwrightInlineScreenshotToolName {
			continue
		}
		identified, ok := tool.(mcpclient.MCPToolIdentifier)
		if !ok {
			continue
		}
		return append(tools, &playwrightScreenshotAttachmentTool{
			base:     tool,
			configID: identified.MCPServerConfigID(),
			store:    store,
		})
	}
	return tools
}

func (t *playwrightScreenshotAttachmentTool) MCPServerConfigID() uuid.UUID {
	return t.configID
}

func (t *playwrightScreenshotAttachmentTool) Info() fantasy.ToolInfo {
	info := t.base.Info()
	info.Name = playwrightSaveScreenshotToolName
	info.Description = "Take a Playwright screenshot and attach it to the current chat as a durable downloadable file under an explicit filename. Use this only when the user explicitly asks to save, download, or name the screenshot. The screenshot is also shown inline."
	info.Parallel = false

	filenameSchema := map[string]any{
		"type":        "string",
		"description": "Filename for the downloadable screenshot, for example cnn-homepage.png.",
	}
	info.Parameters = maps.Clone(info.Parameters)
	if nested, ok := info.Parameters["properties"].(map[string]any); ok {
		nested = maps.Clone(nested)
		if props, ok := nested["properties"].(map[string]any); ok {
			props = maps.Clone(props)
			props["filename"] = filenameSchema
			nested["properties"] = props
			required, _ := nested["required"].([]string)
			nested["required"] = appendRequired(slices.Clone(required), "filename")
			info.Parameters["properties"] = nested
			return info
		}
	}
	info.Parameters["filename"] = filenameSchema
	info.Required = appendRequired(slices.Clone(info.Required), "filename")
	return info
}

func appendRequired(required []string, name string) []string {
	if slices.Contains(required, name) {
		return required
	}
	return append(required, name)
}

func (t *playwrightScreenshotAttachmentTool) Run(
	ctx context.Context,
	call fantasy.ToolCall,
) (fantasy.ToolResponse, error) {
	var input map[string]any
	if strings.TrimSpace(call.Input) != "" {
		if err := json.Unmarshal([]byte(call.Input), &input); err != nil {
			return fantasy.NewTextErrorResponse("invalid JSON input: " + err.Error()), nil
		}
	}

	filename, cleanedInput := screenshotAttachmentInput(input)
	if filename == "" {
		return fantasy.NewTextErrorResponse("filename is required"), nil
	}
	encoded, err := json.Marshal(cleanedInput)
	if err != nil {
		return fantasy.NewTextErrorResponse("encode screenshot input: " + err.Error()), nil
	}

	response, err := t.base.Run(ctx, fantasy.ToolCall{
		ID:    call.ID,
		Name:  playwrightInlineScreenshotToolName,
		Input: string(encoded),
	})
	if err != nil || response.IsError {
		return response, err
	}
	if len(response.Data) == 0 || !strings.HasPrefix(response.MediaType, "image/") {
		return fantasy.NewTextErrorResponse("Playwright did not return screenshot image data"), nil
	}

	attachment, err := t.store(ctx, filename, filename, response.Data)
	if err != nil {
		return fantasy.NewTextErrorResponse("store screenshot attachment: " + err.Error()), nil
	}
	response = chattool.WithAttachments(response, attachment)
	response.Content = strings.TrimSpace(response.Content)
	if response.Content == "" {
		response.Content = fmt.Sprintf("Screenshot saved as %s", attachment.Name)
	} else {
		response.Content += fmt.Sprintf("\nSaved as downloadable attachment: %s", attachment.Name)
	}
	return response, nil
}

func screenshotAttachmentInput(input map[string]any) (string, map[string]any) {
	cleaned := maps.Clone(input)
	if nested, ok := cleaned["properties"].(map[string]any); ok {
		nested = maps.Clone(nested)
		filename, _ := nested["filename"].(string)
		delete(nested, "filename")
		cleaned["properties"] = nested
		return strings.TrimSpace(filename), cleaned
	}
	filename, _ := cleaned["filename"].(string)
	delete(cleaned, "filename")
	return strings.TrimSpace(filename), cleaned
}

func (t *playwrightScreenshotAttachmentTool) ProviderOptions() fantasy.ProviderOptions {
	return t.base.ProviderOptions()
}

func (t *playwrightScreenshotAttachmentTool) SetProviderOptions(options fantasy.ProviderOptions) {
	t.base.SetProviderOptions(options)
}
