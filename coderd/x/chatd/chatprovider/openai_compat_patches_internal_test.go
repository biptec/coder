//nolint:testpackage // These tests cover unexported request-patch guards.
package chatprovider

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/internal/googleopenai"
)

func TestPatchOpenAICompatChatCompletionsBody_Guards(t *testing.T) {
	t.Parallel()

	t.Run("leaves multi tool specific choice unchanged", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{
			"tools": []any{
				functionTool("first_tool"),
				functionTool("second_tool"),
			},
			"tool_choice": map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": "first_tool",
				},
			},
		}

		patched := patchOpenAICompatChatCompletionsBody(mustJSON(t, payload), "http://example.com/v1", "test-model")
		body := decodeJSONMap(t, patched)
		toolChoice, ok := body["tool_choice"].(map[string]any)
		require.True(t, ok)
		function, ok := toolChoice["function"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "first_tool", function["name"])
	})

	t.Run("leaves string tool choice unchanged", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{
			"tools":       []any{functionTool("first_tool")},
			"tool_choice": "auto",
		}

		patched := patchOpenAICompatChatCompletionsBody(mustJSON(t, payload), "http://example.com/v1", "test-model")
		body := decodeJSONMap(t, patched)
		require.Equal(t, "auto", body["tool_choice"])
	})

	t.Run("leaves Gemini assistant history without a user turn unchanged", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{
			"messages": []any{
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						functionToolCall("call_without_user", "history_tool"),
					},
				},
			},
		}

		patched := patchOpenAICompatChatCompletionsBody(mustJSON(t, payload), "https://generativelanguage.googleapis.com/v1beta/openai/", "gemini-3.5-flash")
		body := decodeJSONMap(t, patched)
		messages := body["messages"].([]any)
		require.Empty(t, googleThoughtSignature(t, messages[0], 0))
	})

	t.Run("preserves existing Gemini thought signature", func(t *testing.T) {
		t.Parallel()

		payload := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": "current turn"},
				map[string]any{
					"role": "assistant",
					"tool_calls": []any{
						map[string]any{
							"id":   "call_with_signature",
							"type": "function",
							"function": map[string]any{
								"name":      "signed_tool",
								"arguments": `{}`,
							},
							"extra_content": map[string]any{
								"google": map[string]any{
									"thought_signature": "real-signature",
								},
							},
						},
					},
				},
			},
		}

		patched := patchOpenAICompatChatCompletionsBody(mustJSON(t, payload), "https://generativelanguage.googleapis.com/v1beta/openai/", "gemini-3.5-flash")
		body := decodeJSONMap(t, patched)
		messages := body["messages"].([]any)
		require.Equal(t, "real-signature", googleThoughtSignature(t, messages[1], 0))
	})

	t.Run("strips LiteLLM signature suffix from current Gemini tool IDs", func(t *testing.T) {
		t.Parallel()
		encodedID := "call-1__thought__provider-signature"
		payload := map[string]any{
			"messages": []any{
				map[string]any{"role": "user", "content": "current turn"},
				map[string]any{
					"role":       "assistant",
					"tool_calls": []any{functionToolCall(encodedID, "write_file")},
				},
				map[string]any{"role": "tool", "tool_call_id": encodedID, "content": `{}`},
			},
		}
		patched := patchOpenAICompatChatCompletionsBody(mustJSON(t, payload), "http://coder-aibridge/v1", "gemini/gemini-3.5-flash-lite")
		body := decodeJSONMap(t, patched)
		messages := body["messages"].([]any)
		assistant := messages[1].(map[string]any)
		toolCall := assistant["tool_calls"].([]any)[0].(map[string]any)
		require.Equal(t, "call-1", toolCall["id"])
		require.Equal(t, googleopenai.DummyThoughtSignature, googleThoughtSignature(t, assistant, 0))
		toolResult := messages[2].(map[string]any)
		require.Equal(t, "call-1", toolResult["tool_call_id"])
	})

	t.Run("leaves LiteLLM-style IDs unchanged for non-Gemini models", func(t *testing.T) {
		t.Parallel()
		encodedID := "call-1__thought__opaque"
		payload := map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": "current turn"},
			map[string]any{"role": "assistant", "tool_calls": []any{functionToolCall(encodedID, "tool")}},
			map[string]any{"role": "tool", "tool_call_id": encodedID, "content": `{}`},
		}}
		patched := patchOpenAICompatChatCompletionsBody(mustJSON(t, payload), "http://coder-aibridge/v1", "openai/gpt-oss-120b")
		body := decodeJSONMap(t, patched)
		messages := body["messages"].([]any)
		toolCall := messages[1].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
		require.Equal(t, encodedID, toolCall["id"])
		require.Equal(t, encodedID, messages[2].(map[string]any)["tool_call_id"])
	})
}

func functionTool(name string) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": name,
		},
	}
}

func functionToolCall(id string, name string) map[string]any {
	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": `{}`,
		},
	}
}

func mustJSON(t *testing.T, payload map[string]any) []byte {
	t.Helper()

	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return body
}

func decodeJSONMap(t *testing.T, body []byte) map[string]any {
	t.Helper()

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	return payload
}

func googleThoughtSignature(t *testing.T, rawMessage any, toolCallIndex int) string {
	t.Helper()

	message, ok := rawMessage.(map[string]any)
	require.True(t, ok)
	toolCalls, ok := message["tool_calls"].([]any)
	require.True(t, ok)
	require.Greater(t, len(toolCalls), toolCallIndex)
	toolCall, ok := toolCalls[toolCallIndex].(map[string]any)
	require.True(t, ok)
	extraContent, _ := toolCall["extra_content"].(map[string]any)
	google, _ := extraContent["google"].(map[string]any)
	signature, _ := google["thought_signature"].(string)
	return signature
}
