package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/xerrors"
)

const selectionInstructions = `You are evaluating an MCP tool catalog. Choose exactly one tool that should perform the next action requested by the user. Follow the documented tool contracts and choose the most specific tool whose documented capabilities satisfy the request. Do not invent arguments that are absent from the selected tool schema. Return a function call only.`

type responsesSelector struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

type responsesRequest struct {
	Model             string          `json:"model"`
	Instructions      string          `json:"instructions"`
	Input             string          `json:"input"`
	Tools             []responsesTool `json:"tools"`
	ToolChoice        string          `json:"tool_choice"`
	ParallelToolCalls bool            `json:"parallel_tool_calls"`
	MaxOutputTokens   int             `json:"max_output_tokens"`
}

type responsesTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

type responsesResponse struct {
	Output []struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Arguments any    `json:"arguments"`
	} `json:"output"`
	Usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
		TotalTokens  int64 `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func newResponsesSelector(baseURL, apiKey, model string) *responsesSelector {
	return &responsesSelector{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
	}
}

func (s *responsesSelector) Select(ctx context.Context, prompt string, tools []toolSpec) (selection, tokenUsage, error) {
	requestTools := make([]responsesTool, 0, len(tools))
	for _, tool := range tools {
		requestTools = append(requestTools, responsesTool{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.Parameters,
			Strict:      false,
		})
	}

	payload := responsesRequest{
		Model:             s.model,
		Instructions:      selectionInstructions,
		Input:             prompt,
		Tools:             requestTools,
		ToolChoice:        "required",
		ParallelToolCalls: false,
		MaxOutputTokens:   512,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return selection{}, tokenUsage{}, xerrors.Errorf("marshal Responses request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return selection{}, tokenUsage{}, xerrors.Errorf("create Responses request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return selection{}, tokenUsage{}, xerrors.Errorf("send Responses request: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return selection{}, tokenUsage{}, xerrors.Errorf("read Responses response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return selection{}, tokenUsage{}, xerrors.Errorf("Responses API returned HTTP %d: %s", resp.StatusCode, compactError(responseBody))
	}

	var decoded responsesResponse
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return selection{}, tokenUsage{}, xerrors.Errorf("decode Responses response: %w", err)
	}
	usage := tokenUsage{
		InputTokens:  decoded.Usage.InputTokens,
		OutputTokens: decoded.Usage.OutputTokens,
		TotalTokens:  decoded.Usage.TotalTokens,
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return selection{}, usage, xerrors.Errorf("Responses API error: %s", decoded.Error.Message)
	}

	calls := make([]selection, 0, 1)
	for _, item := range decoded.Output {
		if item.Type != "function_call" {
			continue
		}
		parsed, err := selectionFromFunctionCall(item.Name, item.Arguments)
		if err != nil {
			return selection{}, usage, err
		}
		calls = append(calls, parsed)
	}
	if len(calls) != 1 {
		return selection{}, usage, xerrors.Errorf("expected exactly one function call, got %d", len(calls))
	}
	return calls[0], usage, nil
}

func selectionFromFunctionCall(name string, arguments any) (selection, error) {
	selected := selection{Tool: name, Arguments: map[string]any{}}
	switch value := arguments.(type) {
	case string:
		selected.RawArgs = value
		if strings.TrimSpace(value) == "" {
			return selected, nil
		}
		if err := json.Unmarshal([]byte(value), &selected.Arguments); err != nil {
			return selection{}, xerrors.Errorf("decode arguments for tool %q: %w", name, err)
		}
	case map[string]any:
		selected.Arguments = value
		raw, _ := json.Marshal(value)
		selected.RawArgs = string(raw)
	case nil:
		return selected, nil
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return selection{}, xerrors.Errorf("encode arguments for tool %q: %w", name, err)
		}
		selected.RawArgs = string(raw)
		if err := json.Unmarshal(raw, &selected.Arguments); err != nil {
			return selection{}, xerrors.Errorf("decode arguments for tool %q: %w", name, err)
		}
	}
	return selected, nil
}

func compactError(data []byte) string {
	const limit = 1024
	value := strings.TrimSpace(string(data))
	if len(value) > limit {
		value = value[:limit] + "..."
	}
	return value
}
