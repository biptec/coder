package toolsdk

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
)

func TestWorkspaceSearchWaitDuration(t *testing.T) {
	t.Parallel()

	maxWait := 30 * time.Second

	wait, err := workspaceSearchWaitDuration(nil, maxWait)
	require.NoError(t, err)
	require.Zero(t, wait, "omitted wait_timeout_ms must not introduce an observation delay")

	zero := 0
	wait, err = workspaceSearchWaitDuration(&zero, maxWait)
	require.NoError(t, err)
	require.Zero(t, wait)

	explicit := 250
	wait, err = workspaceSearchWaitDuration(&explicit, maxWait)
	require.NoError(t, err)
	require.Equal(t, 250*time.Millisecond, wait)

	tooLarge := int(maxWait.Milliseconds()) + 1
	_, err = workspaceSearchWaitDuration(&tooLarge, maxWait)
	require.ErrorContains(t, err, "cannot exceed deployment MCP tool timeout")
}

func TestObserveWorkspaceSearchReturnsInitialResults(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)

	running := workspacesdk.SearchResultsResponse{
		Search: workspacesdk.SearchSessionInfo{
			ID:     "search-1",
			Status: "running",
		},
	}
	withResult := workspacesdk.SearchResultsResponse{
		Search: workspacesdk.SearchSessionInfo{
			ID:          "search-1",
			Status:      "running",
			ResultCount: 1,
		},
		Results: []workspacesdk.SearchResult{{
			Path: "/tmp/a.txt",
			Line: 2,
			Text: "needle",
		}},
	}
	gomock.InOrder(
		conn.EXPECT().SearchResults(gomock.Any(), "search-1", 0, 20).Return(running, nil),
		conn.EXPECT().SearchResults(gomock.Any(), "search-1", 0, 20).Return(withResult, nil),
	)

	got, err := observeWorkspaceSearch(t.Context(), conn, "search-1", 0, 20, 250*time.Millisecond)
	require.NoError(t, err)
	require.Equal(t, withResult, got)
}

func TestObserveWorkspaceSearchImmediateSnapshot(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	want := workspacesdk.SearchResultsResponse{
		Search: workspacesdk.SearchSessionInfo{
			ID:     "search-2",
			Status: "running",
		},
	}
	conn.EXPECT().SearchResults(gomock.Any(), "search-2", 5, 10).Return(want, nil)

	got, err := observeWorkspaceSearch(t.Context(), conn, "search-2", 5, 10, 0)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestWorkspaceSearchAnnotations(t *testing.T) {
	t.Parallel()

	require.Equal(t, mcpReadOnlyNonIdempotentAnnotations, WorkspaceSearchStart.MCPAnnotations)
	require.Equal(t, mcpReadOnlyAnnotations, WorkspaceSearchResults.MCPAnnotations)
	require.Equal(t, mcpReadOnlyAnnotations, WorkspaceSearchList.MCPAnnotations)
	require.Equal(t, mcpReadOnlyAnnotations, WorkspaceSearchStop.MCPAnnotations)
}

func TestWorkspaceSearchSchemas(t *testing.T) {
	t.Parallel()

	require.ElementsMatch(t,
		[]string{"workspace", "root", "query", "mode", "max_results"},
		WorkspaceSearchStart.Schema.Required,
	)
	require.Contains(t, WorkspaceSearchStart.Schema.Properties, "wait_timeout_ms")
	startWait := WorkspaceSearchStart.Schema.Properties["wait_timeout_ms"].(map[string]any)
	require.EqualValues(t, 0, startWait["minimum"])
	require.Contains(t, WorkspaceSearchStart.Description, "initial snapshot")
	require.NotContains(t, WorkspaceSearchStart.Description, "default 1000")
	maxResults := WorkspaceSearchStart.Schema.Properties["max_results"].(map[string]any)
	require.EqualValues(t, 0, maxResults["minimum"])
	require.NotContains(t, maxResults, "maximum")

	require.ElementsMatch(t,
		[]string{"workspace", "search_id", "limit"},
		WorkspaceSearchResults.Schema.Required,
	)
	require.Contains(t, WorkspaceSearchResults.Schema.Properties, "wait_timeout_ms")
	resultsWait := WorkspaceSearchResults.Schema.Properties["wait_timeout_ms"].(map[string]any)
	require.EqualValues(t, 0, resultsWait["minimum"])
	require.Contains(t, WorkspaceSearchResults.Description, "current snapshot immediately")
	limit := WorkspaceSearchResults.Schema.Properties["limit"].(map[string]any)
	require.EqualValues(t, 0, limit["minimum"])
	require.NotContains(t, limit, "maximum")
}
