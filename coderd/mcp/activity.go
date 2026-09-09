package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/codersdk/toolsdk"
)

const defaultActivityLimit = 20

type PersistentActivityStatus string

const (
	PersistentActivityStatusSucceeded   PersistentActivityStatus = "succeeded"
	PersistentActivityStatusFailed      PersistentActivityStatus = "failed"
	PersistentActivityStatusInterrupted PersistentActivityStatus = "interrupted"
)

type PersistentActivityHandle struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
}

type PersistentActivityRecorder interface {
	StartToolActivity(ctx context.Context, userID, toolName, workspace, input string, startedAt time.Time) (PersistentActivityHandle, error)
	FinishToolActivity(ctx context.Context, handle PersistentActivityHandle, status PersistentActivityStatus, finishedAt time.Time) error
}

type ActivityRecord struct {
	ID         string `json:"id"`
	Tool       string `json:"tool"`
	Workspace  string `json:"workspace,omitempty"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	ProcessID  string `json:"process_id,omitempty"`
	SearchID   string `json:"search_id,omitempty"`
	Summary    string `json:"summary"`
	started    time.Time
}

type ActivityStore struct {
	mu     sync.Mutex
	max    int
	byUser map[string][]ActivityRecord
}

func NewActivityStore(max int) *ActivityStore {
	if max <= 0 {
		max = 100
	}
	return &ActivityStore{
		max:    max,
		byUser: make(map[string][]ActivityRecord),
	}
}

func (s *ActivityStore) Start(userID, tool, workspace string) string {
	if s == nil || userID == "" {
		return ""
	}
	now := time.Now().UTC()
	id := uuid.New().String()
	rec := ActivityRecord{
		ID:        id,
		Tool:      tool,
		Workspace: workspace,
		Status:    "running",
		StartedAt: now.Format(time.RFC3339Nano),
		Summary:   activitySummary(tool, workspace),
		started:   now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.byUser[userID] = append(s.byUser[userID], rec)
	s.compactLocked(userID)
	return id
}

func (s *ActivityStore) Finish(userID, id, status string, result *mcp.CallToolResult) {
	if s == nil || userID == "" || id == "" {
		return
	}
	now := time.Now().UTC()
	processID, searchID := activityResourceIDs(result)

	s.mu.Lock()
	defer s.mu.Unlock()
	records := s.byUser[userID]
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].ID != id {
			continue
		}
		records[i].Status = status
		records[i].FinishedAt = now.Format(time.RFC3339Nano)
		records[i].DurationMs = now.Sub(records[i].started).Milliseconds()
		records[i].ProcessID = processID
		records[i].SearchID = searchID
		break
	}
	s.byUser[userID] = records
	s.compactLocked(userID)
}

func (s *ActivityStore) compactLocked(userID string) {
	records := s.byUser[userID]
	completed := 0
	for _, rec := range records {
		if rec.Status != "running" {
			completed++
		}
	}
	if completed <= s.max {
		return
	}
	drop := completed - s.max
	out := make([]ActivityRecord, 0, len(records)-drop)
	for _, rec := range records {
		if drop > 0 && rec.Status != "running" {
			drop--
			continue
		}
		out = append(out, rec)
	}
	s.byUser[userID] = out
}

func (s *ActivityStore) List(userID, workspace string, limit int) []ActivityRecord {
	if s == nil || userID == "" {
		return nil
	}
	if limit <= 0 {
		limit = defaultActivityLimit
	}
	if limit > s.max {
		limit = s.max
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	records := s.byUser[userID]
	out := make([]ActivityRecord, 0, min(limit, len(records)))
	completed := 0
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		if workspace != "" && rec.Workspace != workspace {
			continue
		}
		if rec.Status != "running" {
			if completed >= limit {
				continue
			}
			completed++
		}
		rec.started = time.Time{}
		out = append(out, rec)
	}
	return out
}

func activitySummary(tool, workspace string) string {
	if workspace == "" {
		return tool
	}
	return tool + " on " + workspace
}

func activityResourceIDs(result *mcp.CallToolResult) (processID, searchID string) {
	if result == nil {
		return "", ""
	}
	for _, content := range result.Content {
		text, ok := content.(mcp.TextContent)
		if !ok {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(text.Text), &payload); err != nil {
			continue
		}
		if value, ok := payload["process_id"].(string); ok {
			processID = value
		}
		if value, ok := payload["search_id"].(string); ok {
			searchID = value
		}
	}
	return processID, searchID
}

func activityWorkspace(request mcp.CallToolRequest) string {
	args := request.GetArguments()
	for _, key := range []string{"workspace", "workspace_id"} {
		if value, ok := args[key].(string); ok {
			return value
		}
	}
	return ""
}

func (s *Server) withActivityTracking(tool server.ServerTool, toolName string) server.ServerTool {
	if s.activityStore == nil && s.activityRecorder == nil {
		return tool
	}
	original := tool.Handler
	tool.Handler = func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		workspace := activityWorkspace(request)
		input := persistentActivityInput(request.GetArguments())
		startedAt := time.Now().UTC()

		activityID := ""
		if s.activityStore != nil && s.activityUserID != "" {
			activityID = s.activityStore.Start(s.activityUserID, toolName, workspace)
		}

		var persistent PersistentActivityHandle
		if s.activityRecorder != nil && s.activityUserID != "" && workspace != "" && persistAsToolActivity(toolName) {
			var err error
			persistent, err = s.activityRecorder.StartToolActivity(ctx, s.activityUserID, toolName, workspace, input, startedAt)
			if err != nil {
				s.Logger.Debug(ctx, "start persistent MCP tool activity", slog.Error(err), slog.F("tool", toolName), slog.F("workspace", workspace))
			}
		}

		ctx = toolsdk.WithInvocationTool(ctx, toolName)
		result, err := original(ctx, request)

		memoryStatus := "success"
		persistentStatus := PersistentActivityStatusSucceeded
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), ctx.Err() != nil:
			memoryStatus = "error"
			persistentStatus = PersistentActivityStatusInterrupted
		case err != nil || (result != nil && result.IsError):
			memoryStatus = "error"
			persistentStatus = PersistentActivityStatusFailed
		}
		if s.activityStore != nil && s.activityUserID != "" {
			s.activityStore.Finish(s.activityUserID, activityID, memoryStatus, result)
		}
		if s.activityRecorder != nil && persistent.ID != uuid.Nil {
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			finishErr := s.activityRecorder.FinishToolActivity(finishCtx, persistent, persistentStatus, time.Now().UTC())
			cancel()
			if finishErr != nil {
				s.Logger.Debug(context.Background(), "finish persistent MCP tool activity", slog.Error(finishErr), slog.F("tool", toolName), slog.F("workspace_id", persistent.WorkspaceID))
			}
		}
		return result, err
	}
	return tool
}

const maxPersistentActivityInputBytes = 4096

func persistentActivityInput(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	sanitized := make(map[string]any, len(args))
	for key, value := range args {
		sanitized[key] = sanitizePersistentActivityValue(key, value, 0)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(sanitized); err != nil {
		return "{}"
	}
	return truncatePersistentActivityInput(strings.TrimSuffix(buffer.String(), "\n"), maxPersistentActivityInputBytes)
}

func sanitizePersistentActivityValue(key string, value any, depth int) any {
	lowerKey := strings.ToLower(key)
	if isSensitiveActivityInputKey(lowerKey) {
		if text, ok := value.(string); ok {
			return fmt.Sprintf("<redacted %d bytes>", len(text))
		}
		return "<redacted>"
	}
	if depth >= 4 {
		return "<nested value omitted>"
	}

	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for nestedKey, nestedValue := range typed {
			out[nestedKey] = sanitizePersistentActivityValue(nestedKey, nestedValue, depth+1)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizePersistentActivityValue("", item, depth+1))
		}
		return out
	default:
		return value
	}
}

func isSensitiveActivityInputKey(key string) bool {
	switch key {
	case "authorization", "content", "contents", "data", "env", "password", "private_key", "replace", "search", "secret", "stdin", "token", "api_key", "apikey":
		return true
	default:
		return false
	}
}

func truncatePersistentActivityInput(value string, maxBytes int) string {
	if maxBytes <= 3 || len(value) <= maxBytes {
		return value
	}
	end := maxBytes - 3
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + "..."
}

func persistAsToolActivity(toolName string) bool {
	switch toolName {
	case "exec", "bash", "process_start",
		toolsdk.ToolNameWorkspaceExec,
		toolsdk.ToolNameWorkspaceBash,
		toolsdk.ToolNameWorkspaceProcessStart,
		toolsdk.ToolNameWorkspaceProcessStartV2:
		return false
	default:
		return true
	}
}

func (s *Server) registerRecentActivityTool() {
	if s.activityStore == nil || s.activityUserID == "" {
		return
	}
	tool := server.ServerTool{
		Tool: mcp.Tool{
			Name:        "recent_activity",
			Description: "List recent safe tool activity metadata for the authenticated Coder user. No command output, file content, environment, stdin, token, or secret values are stored.",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]any{
					"workspace": map[string]any{
						"type":        "string",
						"description": "Optional exact workspace filter.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum completed history records to return. Defaults to 20, maximum 100. Running records are always included.",
						"minimum":     1,
						"maximum":     100,
					},
				},
			},
			Annotations: mcp.ToolAnnotation{
				ReadOnlyHint:    mcp.ToBoolPtr(true),
				DestructiveHint: mcp.ToBoolPtr(false),
				IdempotentHint:  mcp.ToBoolPtr(true),
				OpenWorldHint:   mcp.ToBoolPtr(false),
			},
		},
	}
	tool.Handler = func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		workspace, _ := args["workspace"].(string)
		limit := defaultActivityLimit
		switch raw := args["limit"].(type) {
		case float64:
			limit = int(raw)
		case int:
			limit = raw
		}
		if limit < 1 || limit > s.activityStore.max {
			return nil, xerrors.Errorf("limit must be between 1 and %d", s.activityStore.max)
		}
		payload := map[string]any{
			"records": s.activityStore.List(s.activityUserID, strings.TrimSpace(workspace), limit),
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(string(data)), nil
	}
	tool = s.withActivityTracking(tool, "recent_activity")
	s.mcpServer.AddTools(tool)
}
