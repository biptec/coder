package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/toolsdk"
)

func TestDeveloperToolAliases(t *testing.T) {
	t.Parallel()

	expected := map[string]string{
		toolsdk.ToolNameGetWorkspace:           "status",
		toolsdk.ToolNameWorkspaceLS:            "list_directory",
		toolsdk.ToolNameWorkspaceReadFile:      "read_file",
		toolsdk.ToolNameWorkspaceWriteFile:     "write_file",
		toolsdk.ToolNameWorkspaceEditFile:      "edit_file",
		toolsdk.ToolNameWorkspaceEditFiles:     "edit_files",
		toolsdk.ToolNameWorkspaceBash:          "bash",
		toolsdk.ToolNameWorkspaceProcessStart:  "process_start",
		toolsdk.ToolNameWorkspaceProcessOutput: "process_output",
		toolsdk.ToolNameWorkspaceProcessList:   "process_list",
		toolsdk.ToolNameWorkspaceProcessSignal: "process_signal",
		toolsdk.ToolNameWorkspaceListApps:      "list_apps",
		toolsdk.ToolNameWorkspacePortForward:   "port_forward",
	}

	require.Len(t, developerToolAliases, len(expected))
	seenNames := map[string]struct{}{}
	for _, alias := range developerToolAliases {
		require.Equal(t, expected[alias.SDKName], alias.MCPName)
		require.NotContains(t, seenNames, alias.MCPName)
		seenNames[alias.MCPName] = struct{}{}
	}
}

func TestReadonlyToolAliases(t *testing.T) {
	t.Parallel()

	allowed := map[string]struct{}{
		toolsdk.ToolNameGetWorkspace:           {},
		toolsdk.ToolNameWorkspaceLS:            {},
		toolsdk.ToolNameWorkspaceReadFile:      {},
		toolsdk.ToolNameWorkspaceProcessOutput: {},
		toolsdk.ToolNameWorkspaceProcessList:   {},
		toolsdk.ToolNameWorkspaceListApps:      {},
	}

	developer := make(map[string]string, len(developerToolAliases))
	for _, alias := range developerToolAliases {
		developer[alias.SDKName] = alias.MCPName
	}

	require.Len(t, readonlyToolAliases, len(allowed))
	for _, alias := range readonlyToolAliases {
		require.Contains(t, allowed, alias.SDKName)
		require.Equal(t, developer[alias.SDKName], alias.MCPName)
	}
}
