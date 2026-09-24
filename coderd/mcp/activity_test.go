//nolint:testpackage // tests intentionally exercise unexported activity-store helpers.
package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/toolsdk"
)

func TestActivityStoreRetentionAndRunningVisibility(t *testing.T) {
	t.Parallel()

	store := NewActivityStore(3)
	userID := "user-a"

	for i := 0; i < 5; i++ {
		id := store.Start(userID, "start_process", "owner/workspace")
		result := mcpgo.NewToolResultText(fmt.Sprintf(`{"process_id":"process-%d","ignored_secret":"do-not-store"}`, i))
		store.Finish(userID, id, "success", result)
	}
	runningID := store.Start(userID, "exec", "owner/workspace")
	require.NotEmpty(t, runningID)

	records := store.List(userID, "owner/workspace", 3)
	require.Len(t, records, 3, "limit applies to all records, including running calls")
	require.Equal(t, runningID, records[0].ID)
	require.Equal(t, "running", records[0].Status)
	require.Empty(t, records[0].FinishedAt)

	completed := 0
	for _, rec := range records {
		require.NotContains(t, rec.Summary, "do-not-store")
		if rec.Status != "running" {
			completed++
			require.NotEmpty(t, rec.ProcessID)
		}
	}
	require.Equal(t, 2, completed)
	require.Empty(t, store.List("different-user", "owner/workspace", 3), "activity must be isolated by authenticated user")
}

func TestActivityStorePageStableCursor(t *testing.T) {
	t.Parallel()

	store := NewActivityStore(20)
	userID := "user-a"
	workspace := "owner/workspace"
	ids := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		id := store.Start(userID, "read_file", workspace)
		store.Finish(userID, id, "success", nil)
		ids = append(ids, id)
	}

	first, err := store.Page(userID, workspace, 2, "")
	require.NoError(t, err)
	require.Len(t, first.Records, 2)
	require.Equal(t, []string{ids[4], ids[3]}, []string{first.Records[0].ID, first.Records[1].ID})
	require.True(t, first.HasMore)
	require.NotEmpty(t, first.NextCursor)

	// A new record arriving after page one must not shift the continuation.
	newID := store.Start(userID, "write_file", workspace)
	store.Finish(userID, newID, "success", nil)

	second, err := store.Page(userID, workspace, 2, first.NextCursor)
	require.NoError(t, err)
	require.Len(t, second.Records, 2)
	require.Equal(t, []string{ids[2], ids[1]}, []string{second.Records[0].ID, second.Records[1].ID})
	require.True(t, second.HasMore)
	require.NotEmpty(t, second.NextCursor)

	third, err := store.Page(userID, workspace, 2, second.NextCursor)
	require.NoError(t, err)
	require.Len(t, third.Records, 1)
	require.Equal(t, ids[0], third.Records[0].ID)
	require.False(t, third.HasMore)
	require.Empty(t, third.NextCursor)

	_, err = store.Page(userID, workspace, 2, "not-a-cursor")
	require.ErrorContains(t, err, "invalid activity cursor")

	_, err = store.Page(userID, "owner/other", 2, first.NextCursor)
	require.ErrorContains(t, err, "no longer available")
}

func TestActivityStorePageZeroReturnsAllRetainedRecords(t *testing.T) {
	t.Parallel()

	store := NewActivityStore(30)
	const (
		userID    = "user-a"
		workspace = "owner/workspace"
	)
	for i := 0; i < 25; i++ {
		id := store.Start(userID, "read_file", workspace)
		store.Finish(userID, id, "success", nil)
	}

	page, err := store.Page(userID, workspace, 0, "")
	require.NoError(t, err)
	require.Len(t, page.Records, 25, "limit=0 must not fall back to the legacy default of 20")
	require.False(t, page.HasMore)
	require.Empty(t, page.NextCursor)
}

func TestActivityTrackingPropagatesInvocationMetadata(t *testing.T) {
	t.Parallel()

	s := &Server{activityStore: NewActivityStore(20), activityUserID: "user-a"}
	expectedTraceID := uuid.New()
	var gotTool string
	var gotTraceID uuid.UUID
	wrapped := s.withActivityTracking(server.ServerTool{
		Handler: func(ctx context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			gotTool = toolsdk.InvocationToolFromContext(ctx)
			gotTraceID, _ = toolsdk.MCPTraceIDFromContext(ctx)
			return mcpgo.NewToolResultText("ok"), nil
		},
	}, "exec")

	ctx := WithTraceID(context.Background(), expectedTraceID)
	_, err := wrapped.Handler(ctx, mcpgo.CallToolRequest{})
	require.NoError(t, err)
	require.Equal(t, "exec", gotTool)
	require.Equal(t, expectedTraceID, gotTraceID)
}

type fakePersistentActivityRecorder struct {
	starts       []string
	inputs       []string
	correlations []string
	persistTools []bool
	finishes     []PersistentActivityStatus
	outputs      []string
}

func (f *fakePersistentActivityRecorder) StartToolActivity(_ context.Context, _ string, toolName, workspace, input, correlationHash string, _ time.Time, persistTool bool) (PersistentActivityHandle, error) {
	f.starts = append(f.starts, toolName+"@"+workspace)
	f.inputs = append(f.inputs, input)
	f.correlations = append(f.correlations, correlationHash)
	f.persistTools = append(f.persistTools, persistTool)
	return PersistentActivityHandle{ID: uuid.New(), WorkspaceID: uuid.New(), PersistTool: persistTool}, nil
}

func (f *fakePersistentActivityRecorder) FinishToolActivity(_ context.Context, _ PersistentActivityHandle, status PersistentActivityStatus, output string, _ time.Time) error {
	f.finishes = append(f.finishes, status)
	f.outputs = append(f.outputs, output)
	return nil
}

func TestActivityTrackingPersistsWorkspaceToolCallsWithoutDuplicatingCommands(t *testing.T) {
	t.Parallel()

	recorder := &fakePersistentActivityRecorder{}
	s := &Server{activityUserID: "user-a", activityRecorder: recorder}
	request := mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{Arguments: map[string]any{
		"workspace":       "owner/workspace",
		"process_id":      "process-123",
		"wait_timeout_ms": float64(10_000),
		"stdin":           "never persist this",
	}}}
	okHandler := func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultText("ok"), nil
	}

	processOutput := s.withActivityTracking(server.ServerTool{Handler: okHandler}, "read_process_output")
	_, err := processOutput.Handler(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, []string{"read_process_output@owner/workspace"}, recorder.starts)
	require.Len(t, recorder.inputs, 1)
	require.Contains(t, recorder.inputs[0], `"process_id":"process-123"`)
	require.Contains(t, recorder.inputs[0], `"wait_timeout_ms":10000`)
	require.Contains(t, recorder.inputs[0], `"stdin":"never persist this"`)
	require.Equal(t, []PersistentActivityStatus{PersistentActivityStatusSucceeded}, recorder.finishes)
	require.Equal(t, []string{""}, recorder.outputs)
	require.Equal(t, []string{""}, recorder.correlations)

	execRequest := mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{Arguments: map[string]any{
		"workspace": "owner/workspace",
		"argv":      []any{"echo", "hello"},
	}}}
	execTool := s.withActivityTracking(server.ServerTool{Handler: okHandler}, "exec")
	_, err = execTool.Handler(context.Background(), execRequest)
	require.NoError(t, err)
	require.Equal(t, []string{"read_process_output@owner/workspace", "exec@owner/workspace"}, recorder.starts)
	require.Equal(t, []bool{true, false}, recorder.persistTools, "command tools need an MCP request span but no duplicate user-facing tool row")
	require.Equal(t, []string{"", toolsdk.CommandActivityCorrelation("", []string{"echo", "hello"})}, recorder.correlations)
	require.Len(t, recorder.finishes, 2)
	require.Equal(t, []string{"", "ok"}, recorder.outputs)
}

func TestPersistentActivityContentIsCompleteAndSecretsAreRedacted(t *testing.T) {
	t.Parallel()

	large := strings.Repeat("activity-content-", 6_000)
	input := persistentActivityInput(map[string]any{
		"workspace": "owner/workspace",
		"stdin":     large,
		"search":    "literal search value",
		"replace":   "literal replacement value",
		"data":      "literal data value",
		"content":   "literal content value",
		"nested": map[string]any{
			"level2": map[string]any{
				"level3": map[string]any{
					"level4": map[string]any{"value": "deep value"},
				},
			},
		},
		"token": "top-secret",
		"env": map[string]any{
			"SAFE_FLAG": "visible",
			"API_TOKEN": "raw-secret",
		},
	})
	require.Contains(t, input, large)
	require.Contains(t, input, `"search":"literal search value"`)
	require.Contains(t, input, `"replace":"literal replacement value"`)
	require.Contains(t, input, `"data":"literal data value"`)
	require.Contains(t, input, `"content":"literal content value"`)
	require.Contains(t, input, `"value":"deep value"`)
	require.NotContains(t, input, "top-secret")
	require.NotContains(t, input, "raw-secret")
	require.Contains(t, input, `"SAFE_FLAG":"visible"`)
	require.Contains(t, input, `"API_TOKEN":"***REDACTED***"`)

	result := mcpgo.NewToolResultText(large)
	require.Equal(t, large, persistentActivityOutput("read_file", result))
	require.Empty(t, persistentActivityOutput("read_process_output", result))
}

func TestPersistAsToolActivity(t *testing.T) {
	t.Parallel()

	for _, toolName := range []string{
		"exec",
		"execute_shell_command",
		"start_process",
		toolsdk.ToolNameWorkspaceExec,
		toolsdk.ToolNameWorkspaceBash,
		toolsdk.ToolNameWorkspaceProcessStart,
		toolsdk.ToolNameWorkspaceProcessStartV2,
	} {
		require.False(t, persistAsToolActivity(toolName), toolName)
	}
	for _, toolName := range []string{
		"read_process_output",
		"list_sessions",
		"read_file",
		"get_search_results",
		toolsdk.ToolNameWorkspaceProcessOutput,
	} {
		require.True(t, persistAsToolActivity(toolName), toolName)
	}
}

func TestActivityToolNames(t *testing.T) {
	t.Parallel()

	developer := ActivityToolNames(codersdk.MCPToolsetDeveloper)
	require.True(t, sort.StringsAreSorted(developer))
	require.Len(t, developer, 29)
	for _, toolName := range []string{"start_process", "execute_shell_command", "read_process_output", "read_file", "get_workspace_capabilities", "list_recent_tool_calls", "find_symbol", "find_references", "find_implementations", "get_diagnostics"} {
		require.Contains(t, developer, toolName)
	}
	require.NotContains(t, developer, "exec")

	readonly := ActivityToolNames(codersdk.MCPToolsetReadonly)
	require.Contains(t, readonly, "read_process_output")
	require.Contains(t, readonly, "read_file")
	require.Contains(t, readonly, "get_workspace_capabilities")
	require.Contains(t, readonly, "list_recent_tool_calls")
	require.NotContains(t, readonly, "exec")
	require.NotContains(t, readonly, "write_file")
	require.NotContains(t, readonly, "signal_process")
}

func TestActivityStoreWorkspaceFilter(t *testing.T) {
	t.Parallel()

	store := NewActivityStore(100)
	userID := "user-a"
	idA := store.Start(userID, "read_file", "owner/a")
	store.Finish(userID, idA, "success", nil)
	idB := store.Start(userID, "read_file", "owner/b")
	store.Finish(userID, idB, "error", nil)

	records := store.List(userID, "owner/a", 20)
	require.Len(t, records, 1)
	require.Equal(t, "owner/a", records[0].Workspace)
	require.Equal(t, "success", records[0].Status)
}
