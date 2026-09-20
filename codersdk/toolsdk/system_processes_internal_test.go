package toolsdk

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func TestPaginateSystemProcesses(t *testing.T) {
	t.Parallel()

	processes := []workspacesdk.SystemProcessInfo{
		{PID: 30, Username: "coder", Command: "python worker.py", StartedAtUnix: 300},
		{PID: 10, Username: "root", Command: "init", StartedAtUnix: 100},
		{PID: 20, Username: "coder", Command: "node server.js", StartedAtUnix: 400},
		{PID: 40, Username: "coder", Command: "postgres", StartedAtUnix: 200},
	}

	first, err := paginateSystemProcesses(processes, "coder", "", 2)
	require.NoError(t, err)
	require.Equal(t, []int32{20, 30}, []int32{first.Processes[0].PID, first.Processes[1].PID}, "newest processes must be first")
	require.True(t, first.HasMore)
	require.NotEmpty(t, first.NextCursor)

	second, err := paginateSystemProcesses(processes, "coder", first.NextCursor, 2)
	require.NoError(t, err)
	require.Len(t, second.Processes, 1)
	require.Equal(t, int32(40), second.Processes[0].PID)
	require.False(t, second.HasMore)
	require.Empty(t, second.NextCursor)

	byCommand, err := paginateSystemProcesses(processes, "SERVER", "", 10)
	require.NoError(t, err)
	require.Len(t, byCommand.Processes, 1)
	require.Equal(t, int32(20), byCommand.Processes[0].PID)
}

func TestPaginateSystemProcessesUnlimitedByDefaultAndRejectsInvalidCursor(t *testing.T) {
	t.Parallel()

	processes := []workspacesdk.SystemProcessInfo{
		{PID: 1, StartedAtUnix: 10},
		{PID: 2, StartedAtUnix: 20},
		{PID: 3, StartedAtUnix: 30},
	}
	all, err := paginateSystemProcesses(processes, "", "", 0)
	require.NoError(t, err)
	require.Equal(t, []int32{3, 2, 1}, []int32{all.Processes[0].PID, all.Processes[1].PID, all.Processes[2].PID})
	require.False(t, all.HasMore)
	require.Empty(t, all.NextCursor)

	_, err = paginateSystemProcesses(nil, "", "not-a-cursor", 1)
	require.ErrorContains(t, err, "invalid process cursor")

	_, err = paginateSystemProcesses(nil, "", "", -1)
	require.ErrorContains(t, err, "limit cannot be negative")
}

func TestWorkspaceListSystemProcessesSchema(t *testing.T) {
	t.Parallel()

	schema := WorkspaceListSystemProcesses.Schema
	require.ElementsMatch(t, []string{"workspace"}, schema.Required)
	require.Contains(t, schema.Properties, "cursor")
	require.Contains(t, schema.Properties, "filter")
	limit := schema.Properties["limit"].(map[string]any)
	require.EqualValues(t, 1, limit["minimum"])
	require.NotContains(t, limit, "maximum")
	require.Equal(t, ToolNameWorkspaceListSystemProcesses, WorkspaceListSystemProcesses.Name)
	require.Equal(t, mcpReadOnlyAnnotations, WorkspaceListSystemProcesses.MCPAnnotations)
}
