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

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/workspaceactivityredact"
	"github.com/coder/coder/v2/codersdk/toolsdk"
)

const (
	defaultActivityLimit                = 20
	persistentActivityHeartbeatInterval = 10 * time.Second
	persistentActivityHeartbeatTimeout  = 2 * time.Second
)

type PersistentActivityStatus string

const (
	PersistentActivityStatusSucceeded   PersistentActivityStatus = "succeeded"
	PersistentActivityStatusFailed      PersistentActivityStatus = "failed"
	PersistentActivityStatusInterrupted PersistentActivityStatus = "interrupted"
)

type PersistentActivityHandle struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	PersistTool bool
}

type PersistentActivityRecorder interface {
	// StartToolActivity always records the MCP request span. persistTool controls
	// whether the same invocation also receives a user-facing tool row; command
	// tools (exec/bash/process_start) are represented by the Agent command row.
	StartToolActivity(ctx context.Context, userID, toolName, workspace, input, correlationHash string, startedAt time.Time, persistTool bool) (PersistentActivityHandle, error)
	FinishToolActivity(ctx context.Context, handle PersistentActivityHandle, status PersistentActivityStatus, output string, finishedAt time.Time) error
}

// PersistentActivityHeartbeater is optional so alternate/test recorders do not
// need to implement periodic persistence. The production recorder implements it
// to distinguish a genuinely long-running MCP request from an orphaned running
// row left behind by a panic, process loss, or failed final write.
type PersistentActivityHeartbeater interface {
	HeartbeatToolActivity(ctx context.Context, handle PersistentActivityHandle, heartbeatAt time.Time) error
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
		arguments := request.GetArguments()
		input := persistentActivityInput(arguments)
		correlationHash := persistentActivityCorrelation(toolName, arguments)
		startedAt := time.Now().UTC()

		activityID := ""
		if s.activityStore != nil && s.activityUserID != "" {
			activityID = s.activityStore.Start(s.activityUserID, toolName, workspace)
		}

		var persistent PersistentActivityHandle
		if s.activityRecorder != nil && s.activityUserID != "" && workspace != "" {
			var err error
			persistent, err = s.activityRecorder.StartToolActivity(
				ctx,
				s.activityUserID,
				toolName,
				workspace,
				input,
				correlationHash,
				startedAt,
				persistAsToolActivity(toolName),
			)
			if err != nil {
				s.Logger.Debug(ctx, "start persistent MCP request activity", slog.Error(err), slog.F("tool", toolName), slog.F("workspace", workspace))
			}
		}

		stopHeartbeat := func() {}
		if persistent.ID != uuid.Nil {
			stopHeartbeat = s.startPersistentActivityHeartbeat(persistent, toolName)
			defer stopHeartbeat()
		}

		ctx = toolsdk.WithInvocationTool(ctx, toolName)
		result, err := original(ctx, request)
		// Stop heartbeats before finalizing. The returned cancel function is
		// idempotent and is also deferred so a panic cannot leave it running.
		stopHeartbeat()

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
			finishErr := s.activityRecorder.FinishToolActivity(finishCtx, persistent, persistentStatus, persistentActivityOutput(toolName, result), time.Now().UTC())
			cancel()
			if finishErr != nil {
				s.Logger.Debug(context.Background(), "finish persistent MCP request activity", slog.Error(finishErr), slog.F("tool", toolName), slog.F("workspace_id", persistent.WorkspaceID))
			}
		}
		return result, err
	}
	return tool
}

func (s *Server) startPersistentActivityHeartbeat(handle PersistentActivityHandle, toolName string) func() {
	heartbeater, ok := s.activityRecorder.(PersistentActivityHeartbeater)
	if !ok || handle.ID == uuid.Nil || handle.WorkspaceID == uuid.Nil {
		return func() {}
	}

	heartbeatCtx, stop := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(persistentActivityHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case heartbeatAt := <-ticker.C:
				ctx, cancel := context.WithTimeout(heartbeatCtx, persistentActivityHeartbeatTimeout)
				err := heartbeater.HeartbeatToolActivity(ctx, handle, heartbeatAt.UTC())
				cancel()
				if err != nil && heartbeatCtx.Err() == nil {
					s.Logger.Debug(context.Background(), "heartbeat persistent MCP request activity", slog.Error(err), slog.F("tool", toolName), slog.F("workspace_id", handle.WorkspaceID), slog.F("request_id", handle.ID))
				}
			}
		}
	}()
	return stop
}

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
	return strings.TrimSuffix(buffer.String(), "\n")
}

func persistentActivityOutput(toolName string, result *mcp.CallToolResult) string {
	// process_output returns stdout/stderr that already belongs to the canonical
	// command activity row. Keeping a second copy on every observation call both
	// bloats history and makes the same process output appear multiple times.
	if toolName == "process_output" || toolName == toolsdk.ToolNameWorkspaceProcessOutput {
		return ""
	}
	if result == nil {
		return ""
	}
	parts := make([]string, 0, len(result.Content)+1)
	for _, content := range result.Content {
		switch typed := content.(type) {
		case mcp.TextContent:
			parts = append(parts, strings.ToValidUTF8(typed.Text, "\uFFFD"))
		case mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[image content omitted: %s]", typed.MIMEType))
		case mcp.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio content omitted: %s]", typed.MIMEType))
		case mcp.EmbeddedResource:
			switch resource := typed.Resource.(type) {
			case mcp.TextResourceContents:
				parts = append(parts, strings.ToValidUTF8(resource.Text, "\uFFFD"))
			case mcp.BlobResourceContents:
				parts = append(parts, fmt.Sprintf("[embedded binary content omitted: %s]", resource.MIMEType))
			default:
				parts = append(parts, fmt.Sprintf("[embedded content omitted: %T]", resource))
			}
		case mcp.ResourceLink:
			label := typed.URI
			if typed.Name != "" {
				label = fmt.Sprintf("%s (%s)", typed.Name, typed.URI)
			}
			parts = append(parts, "[resource: "+label+"]")
		default:
			parts = append(parts, fmt.Sprintf("[content omitted: %T]", content))
		}
	}
	if result.StructuredContent != nil {
		if data, err := json.Marshal(result.StructuredContent); err == nil {
			structured := strings.ToValidUTF8(string(data), "\uFFFD")
			duplicate := false
			for _, part := range parts {
				if part == structured {
					duplicate = true
					break
				}
			}
			if !duplicate {
				parts = append(parts, structured)
			}
		}
	}
	return workspaceactivityredact.Text(strings.Join(parts, "\n"))
}

func persistentActivityCorrelation(toolName string, args map[string]any) string {
	if persistAsToolActivity(toolName) {
		return ""
	}
	command, _ := args["command"].(string)
	argv, ok := persistentActivityStringSlice(args["argv"])
	if !ok || (command == "" && len(argv) == 0) {
		return ""
	}
	return toolsdk.CommandActivityCorrelation(command, argv)
}

func persistentActivityStringSlice(value any) ([]string, bool) {
	if value == nil {
		return []string{}, true
	}
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, text)
		}
		return result, true
	default:
		return nil, false
	}
}

func sanitizePersistentActivityValue(key string, value any, depth int) any {
	lowerKey := strings.ToLower(key)
	if lowerKey == "env" || lowerKey == "environment" {
		return sanitizePersistentActivityEnvironment(value)
	}
	if isSensitiveActivityInputKey(lowerKey) {
		if text, ok := value.(string); ok {
			return fmt.Sprintf("<redacted %d bytes>", len(text))
		}
		return "<redacted>"
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

func sanitizePersistentActivityEnvironment(value any) any {
	environment := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		for key, item := range typed {
			environment[key] = item
		}
	case map[string]any:
		for key, item := range typed {
			text, ok := item.(string)
			if !ok {
				environment[key] = "<redacted non-string value>"
				continue
			}
			environment[key] = text
		}
	default:
		return "<redacted>"
	}
	return workspaceactivityredact.Environment(environment)
}

func isSensitiveActivityInputKey(key string) bool {
	switch key {
	case "authorization", "password", "private_key", "secret", "token", "api_key", "apikey", "access_key", "credential", "cookie":
		return true
	default:
		return false
	}
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
	// Do not track recent_activity in the in-memory store it reads. Tracking it
	// would make every call report itself as the newest running record and would
	// prevent an otherwise idle user's activity list from ever being empty.
	tool = s.withTraceTracking(tool, "recent_activity")
	s.mcpServer.AddTools(tool)
}
