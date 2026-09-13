package toolsdk_test

import (
	"context"
	"reflect"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/agent/agenttest"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/codersdk/toolsdk"
)

func TestWorkspaceBash(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: Workspace MCP bash tools rely on a Unix-like shell (bash) and POSIX/SSH semantics. Use Linux/macOS or WSL for these tests.")
	}

	t.Run("ValidateArgs", func(t *testing.T) {
		t.Parallel()

		deps := toolsdk.Deps{}
		ctx := context.Background()

		// Test empty workspace name
		args := toolsdk.WorkspaceBashArgs{
			Workspace: "",
			Command:   "echo test",
		}
		_, err := toolsdk.WorkspaceBash.Handler(ctx, deps, args)
		require.Error(t, err)
		require.Contains(t, err.Error(), "workspace name cannot be empty")

		// Test empty command
		args = toolsdk.WorkspaceBashArgs{
			Workspace: "test-workspace",
			Command:   "",
		}
		_, err = toolsdk.WorkspaceBash.Handler(ctx, deps, args)
		require.Error(t, err)
		require.Contains(t, err.Error(), "command cannot be empty")
	})

	t.Run("ErrorScenarios", func(t *testing.T) {
		t.Parallel()

		deps := toolsdk.Deps{}
		ctx := context.Background()

		// Test input validation errors (these should fail before client access)
		t.Run("EmptyWorkspace", func(t *testing.T) {
			args := toolsdk.WorkspaceBashArgs{
				Workspace: "", // Empty workspace should be caught by validation
				Command:   "echo test",
			}
			_, err := toolsdk.WorkspaceBash.Handler(ctx, deps, args)
			require.Error(t, err)
			require.Contains(t, err.Error(), "workspace name cannot be empty")
		})

		t.Run("EmptyCommand", func(t *testing.T) {
			args := toolsdk.WorkspaceBashArgs{
				Workspace: "test-workspace",
				Command:   "", // Empty command should be caught by validation
			}
			_, err := toolsdk.WorkspaceBash.Handler(ctx, deps, args)
			require.Error(t, err)
			require.Contains(t, err.Error(), "command cannot be empty")
		})
	})

	t.Run("ToolMetadata", func(t *testing.T) {
		t.Parallel()

		tool := toolsdk.WorkspaceBash
		require.Equal(t, toolsdk.ToolNameWorkspaceBash, tool.Name)
		require.NotEmpty(t, tool.Description)
		require.Contains(t, tool.Description, "Execute a bash command in a Coder workspace")
		require.Contains(t, tool.Description, "output is trimmed of leading and trailing whitespace")
		require.Contains(t, tool.Description, "only /home/coder is persistent")
		require.Contains(t, tool.Description, "structured advisory")
		require.Contains(t, tool.Schema.Required, "workspace")
		require.Contains(t, tool.Schema.Required, "command")

		// Check that schema has the required properties
		require.Contains(t, tool.Schema.Properties, "workspace")
		require.Contains(t, tool.Schema.Properties, "command")
	})

	t.Run("GenericTool", func(t *testing.T) {
		t.Parallel()

		genericTool := toolsdk.WorkspaceBash.Generic()
		require.Equal(t, toolsdk.ToolNameWorkspaceBash, genericTool.Name)
		require.NotEmpty(t, genericTool.Description)
		require.NotNil(t, genericTool.Handler)
		require.False(t, genericTool.UserClientOptional)
	})
}

func TestAllToolsIncludesBash(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: Workspace MCP bash tools rely on a Unix-like shell (bash) and POSIX/SSH semantics. Use Linux/macOS or WSL for these tests.")
	}

	// Verify that WorkspaceBash is included in the All slice
	found := false
	for _, tool := range toolsdk.All {
		if tool.Name == toolsdk.ToolNameWorkspaceBash {
			found = true
			break
		}
	}
	require.True(t, found, "WorkspaceBash tool should be included in toolsdk.All")
}

func TestWorkspaceBashObservationContract(t *testing.T) {
	t.Parallel()

	argsType := reflect.TypeOf(toolsdk.WorkspaceBashArgs{})
	_, hasTimeout := argsType.FieldByName("TimeoutMs")
	require.False(t, hasTimeout, "bash args must not expose a process lifetime timeout")
	_, hasWaitTimeout := argsType.FieldByName("WaitTimeoutMs")
	require.True(t, hasWaitTimeout, "bash args must expose an observation wait timeout")
	_, hasBackground := argsType.FieldByName("Background")
	require.False(t, hasBackground, "bash background mode must use process_start instead")

	tool := toolsdk.WorkspaceBash
	require.NotContains(t, tool.Schema.Properties, "timeout_ms")
	require.Contains(t, tool.Schema.Properties, "wait_timeout_ms")
	waitSchema := tool.Schema.Properties["wait_timeout_ms"].(map[string]any)
	require.Equal(t, 0, waitSchema["minimum"])
	require.NotContains(t, waitSchema, "default")
	require.NotContains(t, waitSchema, "maximum")
	require.NotContains(t, tool.Schema.Properties, "background")
	require.Contains(t, tool.Description, "no process lifetime timeout")
	require.Contains(t, tool.Description, "waits for completion up to the deployment-wide MCP tool timeout")
	require.Contains(t, tool.Description, "process_id")
	require.Contains(t, tool.Description, "running=true")
	require.Contains(t, tool.Description, "continues independently")
	require.Contains(t, tool.Description, "coder_workspace_process_output")
	require.Contains(t, tool.Description, "coder_workspace_process_start")
	require.NotContains(t, tool.Description, "background: true")
}

func TestWorkspaceBashIntegration(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: Workspace MCP bash tools rely on a Unix-like shell (bash) and POSIX/SSH semantics. Use Linux/macOS or WSL for these tests.")
	}

	client, workspace, agentToken := setupWorkspaceForAgent(t, nil)
	_ = agenttest.New(t, client.URL, agentToken)
	coderdtest.NewWorkspaceAgentWaiter(t, client, workspace.ID).Wait()

	deps, err := toolsdk.NewDeps(client)
	require.NoError(t, err)

	result, err := testTool(t, toolsdk.WorkspaceBash, deps, toolsdk.WorkspaceBashArgs{
		Workspace: workspace.Name,
		Command:   `printf 'before\n'; sleep 0.1; printf 'after\n'`,
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	require.Equal(t, "before\nafter", result.Output)
	require.Empty(t, result.ProcessID)
	require.False(t, result.Running)
}
