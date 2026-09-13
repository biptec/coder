package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/toolsdk"
)

func TestDeveloperToolAliases(t *testing.T) {
	t.Parallel()

	expected := map[string]string{
		toolsdk.ToolNameGetWorkspace:             "status",
		toolsdk.ToolNameListAccessibleWorkspaces: "list_workspaces",
		toolsdk.ToolNameWorkspaceListDirectoryV2: "list_directory",
		toolsdk.ToolNameWorkspaceReadFileV2:      "read_file",
		toolsdk.ToolNameWorkspaceReadFilesV2:     "read_files",
		toolsdk.ToolNameWorkspaceWriteFileV2:     "write_file",
		toolsdk.ToolNameWorkspaceFileInfo:        "file_info",
		toolsdk.ToolNameWorkspaceCreateDirectory: "create_directory",
		toolsdk.ToolNameWorkspaceMoveFile:        "move_file",
		toolsdk.ToolNameWorkspaceCopyPath:        "copy_path",
		toolsdk.ToolNameWorkspaceRemovePath:      "remove_path",
		toolsdk.ToolNameWorkspaceHTTPFetch:       "http_fetch",
		toolsdk.ToolNameWorkspaceCodeQuery:       "code_query",
		toolsdk.ToolNameWorkspaceCodeRename:      "code_rename",
		toolsdk.ToolNameWorkspaceSearchStart:     "search_start",
		toolsdk.ToolNameWorkspaceSearchResults:   "search_results",
		toolsdk.ToolNameWorkspaceSearchList:      "search_list",
		toolsdk.ToolNameWorkspaceSearchStop:      "search_stop",
		toolsdk.ToolNameWorkspaceEditFile:        "edit_file",
		toolsdk.ToolNameWorkspaceEditFiles:       "edit_files",
		toolsdk.ToolNameWorkspaceBash:            "bash",
		toolsdk.ToolNameWorkspaceExec:            "exec",
		toolsdk.ToolNameWorkspaceProcessStartV2:  "process_start",
		toolsdk.ToolNameWorkspaceProcessOutput:   "process_output",
		toolsdk.ToolNameWorkspaceProcessList:     "process_list",
		toolsdk.ToolNameWorkspaceProcessInput:    "process_input",
		toolsdk.ToolNameWorkspaceProcessSignal:   "process_signal",
		toolsdk.ToolNameWorkspaceListApps:        "list_apps",
		toolsdk.ToolNameWorkspaceCapabilities:    "capabilities",
	}

	require.Len(t, developerToolAliases, len(expected))
	seenNames := map[string]struct{}{}
	for _, alias := range developerToolAliases {
		require.Equal(t, expected[alias.SDKName], alias.MCPName)
		require.NotContains(t, seenNames, alias.MCPName)
		seenNames[alias.MCPName] = struct{}{}
	}
}

func TestServerInstructionsPointToDynamicCapabilities(t *testing.T) {
	t.Parallel()

	require.Contains(t, MCPServerInstructions, "inspect the available capabilities with capabilities")
	require.Contains(t, MCPServerInstructions, "refresh it only after the workspace environment changes")
	for _, concreteTool := range []string{"Chromium", "Playwright", "Firefox", "PostgreSQL", "MySQL", "kubectl"} {
		require.NotContains(t, MCPServerInstructions, concreteTool)
	}
}

func TestReadonlyToolAliases(t *testing.T) {
	t.Parallel()

	allowed := map[string]struct{}{
		toolsdk.ToolNameGetWorkspace:             {},
		toolsdk.ToolNameListAccessibleWorkspaces: {},
		toolsdk.ToolNameWorkspaceListDirectoryV2: {},
		toolsdk.ToolNameWorkspaceReadFileV2:      {},
		toolsdk.ToolNameWorkspaceReadFilesV2:     {},
		toolsdk.ToolNameWorkspaceFileInfo:        {},
		toolsdk.ToolNameWorkspaceHTTPFetch:       {},
		toolsdk.ToolNameWorkspaceCodeQuery:       {},
		toolsdk.ToolNameWorkspaceSearchStart:     {},
		toolsdk.ToolNameWorkspaceSearchResults:   {},
		toolsdk.ToolNameWorkspaceSearchList:      {},
		toolsdk.ToolNameWorkspaceSearchStop:      {},
		toolsdk.ToolNameWorkspaceProcessOutput:   {},
		toolsdk.ToolNameWorkspaceProcessList:     {},
		toolsdk.ToolNameWorkspaceListApps:        {},
		toolsdk.ToolNameWorkspaceCapabilities:    {},
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
