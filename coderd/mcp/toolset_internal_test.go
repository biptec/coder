package mcp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/toolsdk"
)

func TestApplyMCPTimeoutMaximum(t *testing.T) {
	t.Parallel()

	properties := map[string]any{
		"wait_timeout_ms": map[string]any{"type": "integer", "minimum": 0},
		"timeout_ms":      map[string]any{"type": "integer", "minimum": 1},
	}
	applyMCPTimeoutMaximum(properties, 123*time.Second)
	require.EqualValues(t, 123000, properties["wait_timeout_ms"].(map[string]any)["maximum"])
	require.EqualValues(t, 123000, properties["timeout_ms"].(map[string]any)["maximum"])
}

func TestMCPFromSDKClonesSchemaBeforeRuntimeRewrites(t *testing.T) {
	t.Parallel()

	sourceWait := toolsdk.WorkspaceExec.Schema.Properties["wait_timeout_ms"].(map[string]any)
	require.NotContains(t, sourceWait, "maximum")

	serverTool := mcpFromSDK(toolsdk.WorkspaceExec.Generic(), toolsdk.Deps{})
	applyMCPTimeoutMaximum(serverTool.Tool.InputSchema.Properties, 123*time.Second)
	serverWait := serverTool.Tool.InputSchema.Properties["wait_timeout_ms"].(map[string]any)
	require.EqualValues(t, 123000, serverWait["maximum"])
	require.NotContains(t, sourceWait, "maximum", "runtime MCP schema rewrites must not mutate shared SDK schemas")
}

func TestAssistantToolsetShape(t *testing.T) {
	t.Parallel()

	require.Len(t, developerToolAliases, 29, "developer aliases plus recent_activity must total 30 tools")
	require.Len(t, ActivityToolNames(codersdk.MCPToolsetDeveloper), 30)
	require.Len(t, readonlyToolAliases, 16)
	require.Len(t, ActivityToolNames(codersdk.MCPToolsetReadonly), 17)

	developer := make(map[string]struct{}, len(developerToolAliases))
	for _, alias := range developerToolAliases {
		_, duplicate := developer[alias.MCPName]
		require.False(t, duplicate, "duplicate developer MCP tool name %q", alias.MCPName)
		developer[alias.MCPName] = struct{}{}
	}

	readonly := make(map[string]struct{}, len(readonlyToolAliases))
	for _, alias := range readonlyToolAliases {
		_, ok := developer[alias.MCPName]
		require.True(t, ok, "readonly tool %q must exist in developer toolset", alias.MCPName)
		readonly[alias.MCPName] = struct{}{}
	}
	for _, readOnly := range []string{"http_fetch", "code_query"} {
		_, ok := readonly[readOnly]
		require.True(t, ok, "readonly toolset must expose %q", readOnly)
	}
	for _, mutating := range []string{"write_file", "create_directory", "move_file", "copy_path", "remove_path", "edit_file", "edit_files", "code_rename", "bash", "exec", "process_start", "process_input", "process_signal"} {
		_, ok := readonly[mutating]
		require.False(t, ok, "readonly toolset must not expose %q", mutating)
	}
	for _, excluded := range []string{"remote_hosts", "http_request", "git_query", "git_mutate"} {
		_, ok := developer[excluded]
		require.False(t, ok, "developer toolset must not expose out-of-scope tool %q", excluded)
	}
}
