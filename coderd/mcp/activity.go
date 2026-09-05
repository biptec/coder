package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/xerrors"
)

const defaultActivityLimit = 20

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
	if s.activityStore == nil || s.activityUserID == "" {
		return tool
	}
	original := tool.Handler
	tool.Handler = func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := s.activityStore.Start(s.activityUserID, toolName, activityWorkspace(request))
		result, err := original(ctx, request)
		status := "success"
		if err != nil || (result != nil && result.IsError) {
			status = "error"
		}
		s.activityStore.Finish(s.activityUserID, id, status, result)
		return result, err
	}
	return tool
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
	s.mcpServer.AddTools(tool)
}
