package chatprovider

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/coder/coder/v2/internal/googleopenai"
)

// OpenAI-compatible providers share an API shape but differ in the exact JSON
// they accept. These patches adjust Fantasy's serialized request body at the
// transport boundary so higher-level generation code can stay provider agnostic.

func withOpenAICompatRequestPatches(
	client *http.Client,
	baseURL string,
	modelID string,
) *http.Client {
	if client == nil {
		client = &http.Client{}
	} else {
		clone := *client
		client = &clone
	}
	client.Transport = &openAICompatRequestPatchTransport{
		Base:    client.Transport,
		BaseURL: baseURL,
		ModelID: modelID,
	}
	return client
}

type openAICompatRequestPatchTransport struct {
	Base http.RoundTripper
	// BaseURL is the configured provider base URL, used to detect direct Gemini endpoints.
	BaseURL string
	// ModelID is the configured model ID, used to detect Gemini routes through Coder AI Bridge.
	ModelID string
}

func (t *openAICompatRequestPatchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base()
	if !shouldPatchOpenAICompatRequest(req) {
		return base.RoundTrip(req)
	}

	body, err := io.ReadAll(req.Body)
	closeErr := req.Body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}

	patched := patchOpenAICompatChatCompletionsBody(body, t.BaseURL, t.ModelID)
	patchedReq := req.Clone(req.Context())
	patchedReq.Body = io.NopCloser(bytes.NewReader(patched))
	patchedReq.ContentLength = int64(len(patched))
	patchedReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(patched)), nil
	}

	return base.RoundTrip(patchedReq)
}

func (t *openAICompatRequestPatchTransport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func shouldPatchOpenAICompatRequest(req *http.Request) bool {
	return req != nil &&
		req.Method == http.MethodPost &&
		req.Body != nil &&
		strings.HasSuffix(req.URL.Path, "/chat/completions")
}

func patchOpenAICompatChatCompletionsBody(body []byte, baseURL string, modelID string) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}

	changed := rewriteOpenAICompatSingleToolChoice(payload)
	if googleopenai.ShouldPatchOpenAICompatRequest(baseURL, modelID) {
		changed = stripLiteLLMThoughtSignaturesFromLatestTurn(payload) || changed
		changed = googleopenai.AddThoughtSignaturesToLatestTurn(payload) || changed
	}
	if !changed {
		return body
	}

	patched, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return patched
}

const liteLLMThoughtSignatureMarker = "__thought__"

// stripLiteLLMThoughtSignaturesFromLatestTurn removes LiteLLM's transport-only
// thought-signature suffix from matching tool call/result IDs. Coder keeps the
// original IDs in chat history; only the outbound provider payload is normalized.
// LiteLLM then applies Google's documented dummy-signature fallback itself.
func stripLiteLLMThoughtSignaturesFromLatestTurn(payload map[string]any) bool {
	messages, ok := payload["messages"].([]any)
	if !ok {
		return false
	}
	currentTurnStart := -1
	for i, raw := range messages {
		message, ok := raw.(map[string]any)
		if ok && message["role"] == "user" {
			currentTurnStart = i
		}
	}
	if currentTurnStart == -1 {
		return false
	}
	changed := false
	for _, raw := range messages[currentTurnStart+1:] {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if toolCallID, ok := message["tool_call_id"].(string); ok {
			clean := strings.SplitN(toolCallID, liteLLMThoughtSignatureMarker, 2)[0]
			if clean != "" && clean != toolCallID {
				message["tool_call_id"] = clean
				changed = true
			}
		}
		toolCalls, _ := message["tool_calls"].([]any)
		for _, rawToolCall := range toolCalls {
			toolCall, ok := rawToolCall.(map[string]any)
			if !ok {
				continue
			}
			id, _ := toolCall["id"].(string)
			clean := strings.SplitN(id, liteLLMThoughtSignatureMarker, 2)[0]
			if clean != "" && clean != id {
				toolCall["id"] = clean
				changed = true
			}
		}
	}
	return changed
}

// rewriteOpenAICompatSingleToolChoice replaces a single named tool choice with
// "required" because some compatible endpoints reject the named object form.
func rewriteOpenAICompatSingleToolChoice(payload map[string]any) bool {
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		return false
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		return false
	}
	function, ok := tool["function"].(map[string]any)
	if !ok {
		return false
	}
	toolName, _ := function["name"].(string)
	if toolName == "" {
		return false
	}

	toolChoice, ok := payload["tool_choice"].(map[string]any)
	if !ok {
		return false
	}
	if toolType, _ := toolChoice["type"].(string); toolType != "function" {
		return false
	}
	choiceFunction, ok := toolChoice["function"].(map[string]any)
	if !ok {
		return false
	}
	choiceName, _ := choiceFunction["name"].(string)
	if choiceName != toolName {
		return false
	}

	payload["tool_choice"] = "required"
	return true
}
