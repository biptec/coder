package mcp

import (
	"context"
	"fmt"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

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
