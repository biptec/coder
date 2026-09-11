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
	runningID := store.Start(userID, "exec", "owner/workspace")
	require.NotEmpty(t, runningID)

	for i := 0; i < 5; i++ {
		id := store.Start(userID, "process_start", "owner/workspace")
		result := mcpgo.NewToolResultText(fmt.Sprintf(`{"process_id":"process-%d","ignored_secret":"do-not-store"}`, i))
		store.Finish(userID, id, "success", result)
	}

	records := store.List(userID, "", 3)
	require.Len(t, records, 4, "three completed records plus the still-running record")

	runningFound := false
	for _, rec := range records {
		if rec.ID == runningID {
			runningFound = true
			require.Equal(t, "running", rec.Status)
			require.Empty(t, rec.FinishedAt)
		}
		require.NotContains(t, rec.Summary, "do-not-store")
	}
	require.True(t, runningFound)

	completed := 0
	for _, rec := range records {
		if rec.Status != "running" {
			completed++
			require.NotEmpty(t, rec.ProcessID)
		}
	}
	require.Equal(t, 3, completed)
	require.Empty(t, store.List("different-user", "", 3), "activity must be isolated by authenticated user")
}

func TestActivityTrackingPropagatesInvocationTool(t *testing.T) {
	t.Parallel()

	s := &Server{activityStore: NewActivityStore(20), activityUserID: "user-a"}
	var gotTool string
	wrapped := s.withActivityTracking(server.ServerTool{
		Handler: func(ctx context.Context, _ mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
			gotTool = toolsdk.InvocationToolFromContext(ctx)
			return mcpgo.NewToolResultText("ok"), nil
		},
	}, "exec")

	_, err := wrapped.Handler(context.Background(), mcpgo.CallToolRequest{})
	require.NoError(t, err)
	require.Equal(t, "exec", gotTool)
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

	processOutput := s.withActivityTracking(server.ServerTool{Handler: okHandler}, "process_output")
	_, err := processOutput.Handler(context.Background(), request)
	require.NoError(t, err)
	require.Equal(t, []string{"process_output@owner/workspace"}, recorder.starts)
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
	require.Equal(t, []string{"process_output@owner/workspace", "exec@owner/workspace"}, recorder.starts)
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
	require.Empty(t, persistentActivityOutput("process_output", result))
}

func TestPersistAsToolActivity(t *testing.T) {
	t.Parallel()

	for _, toolName := range []string{
		"exec",
		"bash",
		"process_start",
		toolsdk.ToolNameWorkspaceExec,
		toolsdk.ToolNameWorkspaceBash,
		toolsdk.ToolNameWorkspaceProcessStart,
		toolsdk.ToolNameWorkspaceProcessStartV2,
	} {
		require.False(t, persistAsToolActivity(toolName), toolName)
	}
	for _, toolName := range []string{
		"process_output",
		"process_list",
		"read_file",
		"search_results",
		toolsdk.ToolNameWorkspaceProcessOutput,
	} {
		require.True(t, persistAsToolActivity(toolName), toolName)
	}
}

func TestActivityToolNames(t *testing.T) {
	t.Parallel()

	developer := ActivityToolNames(codersdk.MCPToolsetDeveloper)
	require.True(t, sort.StringsAreSorted(developer))
	for _, toolName := range []string{"exec", "process_output", "read_file", "recent_activity"} {
		require.Contains(t, developer, toolName)
	}

	readonly := ActivityToolNames(codersdk.MCPToolsetReadonly)
	require.Contains(t, readonly, "process_output")
	require.Contains(t, readonly, "read_file")
	require.Contains(t, readonly, "recent_activity")
	require.NotContains(t, readonly, "exec")
	require.NotContains(t, readonly, "write_file")
	require.NotContains(t, readonly, "process_signal")
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
