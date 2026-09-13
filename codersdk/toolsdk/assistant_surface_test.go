package toolsdk_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/toolsdk"
)

func requireSchemaProperties(t *testing.T, properties map[string]any, names ...string) {
	t.Helper()
	for _, name := range names {
		require.Contains(t, properties, name)
	}
}

func TestAssistantRemoteToolSchemas(t *testing.T) {
	t.Parallel()

	for _, properties := range map[string]map[string]any{
		"exec":             toolsdk.WorkspaceExec.Schema.Properties,
		"bash":             toolsdk.WorkspaceBash.Schema.Properties,
		"process_start":    toolsdk.WorkspaceProcessStartV2.Schema.Properties,
		"list_directory":   toolsdk.WorkspaceListDirectoryV2.Schema.Properties,
		"read_file":        toolsdk.WorkspaceReadFileV2.Schema.Properties,
		"read_files":       toolsdk.WorkspaceReadFilesV2.Schema.Properties,
		"write_file":       toolsdk.WorkspaceWriteFileV2.Schema.Properties,
		"file_info":        toolsdk.WorkspaceFileInfoTool.Schema.Properties,
		"create_directory": toolsdk.WorkspaceCreateDirectory.Schema.Properties,
		"move_file":        toolsdk.WorkspaceMoveFile.Schema.Properties,
		"edit_file":        toolsdk.WorkspaceEditFile.Schema.Properties,
		"edit_files":       toolsdk.WorkspaceEditFiles.Schema.Properties,
		"search_start":     toolsdk.WorkspaceSearchStart.Schema.Properties,
		"http_fetch":       toolsdk.WorkspaceHTTPFetch.Schema.Properties,
	} {
		requireSchemaProperties(t, properties, "host", "identity_file")
		hostDescription := properties["host"].(map[string]any)["description"].(string)
		require.Contains(t, hostDescription, "hostname")
		require.Contains(t, hostDescription, "IP address")
		require.Contains(t, hostDescription, "user@host")
	}

	requireSchemaProperties(t, toolsdk.WorkspaceExec.Schema.Properties, "wait_timeout_ms")
	requireSchemaProperties(t, toolsdk.WorkspaceBash.Schema.Properties, "wait_timeout_ms")
	requireSchemaProperties(t, toolsdk.WorkspaceProcessList.Schema.Properties, "host")
	requireSchemaProperties(t, toolsdk.WorkspaceEditFile.Schema.Properties, "dry_run")
	requireSchemaProperties(t, toolsdk.WorkspaceEditFiles.Schema.Properties, "dry_run")

	copySource := toolsdk.WorkspaceCopyPath.Schema.Properties["source"].(map[string]any)["properties"].(map[string]any)
	copyDestination := toolsdk.WorkspaceCopyPath.Schema.Properties["destination"].(map[string]any)["properties"].(map[string]any)
	requireSchemaProperties(t, copySource, "path", "host", "identity_file")
	requireSchemaProperties(t, copyDestination, "path", "host", "identity_file")

	require.NotContains(t, toolsdk.WorkspaceCodeQuery.Schema.Properties, "host")
	require.NotContains(t, toolsdk.WorkspaceCodeQuery.Schema.Properties, "identity_file")
	require.NotContains(t, toolsdk.WorkspaceCodeRename.Schema.Properties, "host")
	require.NotContains(t, toolsdk.WorkspaceCodeRename.Schema.Properties, "identity_file")
}

func TestAssistantNewToolSchemasAndAnnotations(t *testing.T) {
	t.Parallel()

	requireSchemaProperties(t, toolsdk.WorkspaceCopyPath.Schema.Properties, "source", "destination", "overwrite")
	require.True(t, toolsdk.WorkspaceCopyPath.MCPAnnotations.DestructiveHint)
	require.True(t, toolsdk.WorkspaceCopyPath.MCPAnnotations.OpenWorldHint)

	requireSchemaProperties(t, toolsdk.WorkspaceRemovePath.Schema.Properties, "path", "host", "identity_file", "recursive")
	require.True(t, toolsdk.WorkspaceRemovePath.MCPAnnotations.DestructiveHint)
	require.True(t, toolsdk.WorkspaceRemovePath.MCPAnnotations.OpenWorldHint)

	requireSchemaProperties(t, toolsdk.WorkspaceHTTPFetch.Schema.Properties, "url", "method", "host", "identity_file", "timeout_ms")
	require.True(t, toolsdk.WorkspaceHTTPFetch.MCPAnnotations.ReadOnlyHint)
	require.True(t, toolsdk.WorkspaceHTTPFetch.MCPAnnotations.OpenWorldHint)

	requireSchemaProperties(t, toolsdk.WorkspaceCodeQuery.Schema.Properties, "operation", "path")
	require.True(t, toolsdk.WorkspaceCodeQuery.MCPAnnotations.ReadOnlyHint)
	require.True(t, toolsdk.WorkspaceCodeQuery.MCPAnnotations.OpenWorldHint)

	requireSchemaProperties(t, toolsdk.WorkspaceCodeRename.Schema.Properties, "path", "line", "column", "new_name", "dry_run")
	require.True(t, toolsdk.WorkspaceCodeRename.MCPAnnotations.DestructiveHint)
	require.True(t, toolsdk.WorkspaceCodeRename.MCPAnnotations.OpenWorldHint)
}

func TestAssistantWaitSchemasHaveNoSDKDefaultOrStaticMaximum(t *testing.T) {
	t.Parallel()

	for name, properties := range map[string]map[string]any{
		"exec":           toolsdk.WorkspaceExec.Schema.Properties,
		"bash":           toolsdk.WorkspaceBash.Schema.Properties,
		"process_output": toolsdk.WorkspaceProcessOutput.Schema.Properties,
	} {
		wait := properties["wait_timeout_ms"].(map[string]any)
		require.NotContains(t, wait, "default", name)
		require.NotContains(t, wait, "maximum", name)
	}
}
