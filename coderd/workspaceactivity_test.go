package coderd

import (
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
)

func TestParseWorkspaceCommandActivityQuery(t *testing.T) {
	t.Parallel()

	startedAfter := time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC)
	startedBefore := startedAfter.Add(3 * time.Hour)
	values := url.Values{
		"status":          {"running,succeeded"},
		"tool":            {"exec", "bash"},
		"source":          {"mcp,ssh"},
		"search":          {"needle text"},
		"started_after":   {startedAfter.Format(time.RFC3339Nano)},
		"started_before":  {startedBefore.Format(time.RFC3339Nano)},
		"duration_min_ms": {"250"},
		"duration_max_ms": {"20000"},
		"sort_by":         {"duration"},
		"sort_direction":  {"desc"},
		"page":            {"3"},
		"page_size":       {"100"},
	}

	req, err := parseWorkspaceCommandActivityQuery(values)
	require.NoError(t, err)
	require.Equal(t, []codersdk.WorkspaceCommandActivityStatus{
		codersdk.WorkspaceCommandActivityStatusRunning,
		codersdk.WorkspaceCommandActivityStatusSucceeded,
	}, req.Statuses)
	require.Equal(t, []string{"exec", "bash"}, req.Tools)
	require.Equal(t, []codersdk.WorkspaceCommandActivitySource{
		codersdk.WorkspaceCommandActivitySourceMCP,
		codersdk.WorkspaceCommandActivitySourceSSH,
	}, req.Sources)
	require.Equal(t, "needle text", req.Search)
	require.Equal(t, startedAfter, *req.StartedAfter)
	require.Equal(t, startedBefore, *req.StartedBefore)
	require.EqualValues(t, 250, *req.DurationMinMS)
	require.EqualValues(t, 20_000, *req.DurationMaxMS)
	require.Equal(t, codersdk.WorkspaceCommandActivitySortDuration, req.SortBy)
	require.Equal(t, codersdk.WorkspaceCommandActivitySortDescending, req.SortDirection)
	require.Equal(t, 3, req.Page)
	require.Equal(t, 100, req.PageSize)
}

func TestParseWorkspaceCommandActivityQueryLegacyQAlias(t *testing.T) {
	t.Parallel()

	req, err := parseWorkspaceCommandActivityQuery(url.Values{"q": {"legacy needle"}})
	require.NoError(t, err)
	require.Equal(t, "legacy needle", req.Search)

	req, err = parseWorkspaceCommandActivityQuery(url.Values{
		"search": {"canonical"},
		"q":      {"legacy"},
	})
	require.NoError(t, err)
	require.Equal(t, "canonical", req.Search)
}

func TestWorkspaceCommandActivityFilterValidation(t *testing.T) {
	t.Parallel()

	min := int64(20_000)
	max := int64(1_000)
	_, err := workspaceCommandActivityFilter(codersdk.WorkspaceCommandActivityFilter{
		DurationMinMS: &min,
		DurationMaxMS: &max,
	})
	require.ErrorContains(t, err, "duration_min_ms cannot exceed duration_max_ms")

	after := time.Now().Add(time.Hour)
	before := time.Now()
	_, err = workspaceCommandActivityFilter(codersdk.WorkspaceCommandActivityFilter{
		StartedAfter:  &after,
		StartedBefore: &before,
	})
	require.ErrorContains(t, err, "started_after cannot be after started_before")
}
