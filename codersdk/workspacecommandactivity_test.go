package codersdk

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceCommandActivityRequestQueryValues(t *testing.T) {
	t.Parallel()

	startedAfter := time.Date(2026, 9, 9, 8, 1, 2, 345000000, time.UTC)
	startedBefore := startedAfter.Add(2 * time.Hour)
	minDuration := int64(250)
	maxDuration := int64(20_000)
	req := WorkspaceCommandActivityRequest{
		WorkspaceCommandActivityFilter: WorkspaceCommandActivityFilter{
			Statuses:      []WorkspaceCommandActivityStatus{WorkspaceCommandActivityStatusRunning, WorkspaceCommandActivityStatusSucceeded},
			Tools:         []string{"exec", "bash"},
			Sources:       []WorkspaceCommandActivitySource{WorkspaceCommandActivitySourceMCP, WorkspaceCommandActivitySourceSSH},
			Search:        "needle text",
			StartedAfter:  &startedAfter,
			StartedBefore: &startedBefore,
			DurationMinMS: &minDuration,
			DurationMaxMS: &maxDuration,
		},
		SortBy:        WorkspaceCommandActivitySortDuration,
		SortDirection: WorkspaceCommandActivitySortDescending,
		Page:          3,
		PageSize:      100,
	}

	values := req.queryValues()
	require.Equal(t, []string{"running", "succeeded"}, values["status"])
	require.Equal(t, []string{"exec", "bash"}, values["tool"])
	require.Equal(t, []string{"mcp", "ssh"}, values["source"])
	require.Equal(t, "needle text", values.Get("search"))
	require.Empty(t, values.Get("q"))
	require.Equal(t, startedAfter.Format(time.RFC3339Nano), values.Get("started_after"))
	require.Equal(t, startedBefore.Format(time.RFC3339Nano), values.Get("started_before"))
	require.Equal(t, "250", values.Get("duration_min_ms"))
	require.Equal(t, "20000", values.Get("duration_max_ms"))
	require.Equal(t, "duration", values.Get("sort_by"))
	require.Equal(t, "desc", values.Get("sort_direction"))
	require.Equal(t, "3", values.Get("page"))
	require.Equal(t, "100", values.Get("page_size"))
}
