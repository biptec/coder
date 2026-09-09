package toolsdk_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/agent/agenttest"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/toolsdk"
)

func TestWorkspaceProcessToolsValidateArgs(t *testing.T) {
	t.Parallel()

	deps := toolsdk.Deps{}

	_, err := testTool(t, toolsdk.WorkspaceProcessStart, deps, toolsdk.WorkspaceProcessStartArgs{
		Workspace: "test-workspace",
	})
	require.ErrorContains(t, err, "command cannot be empty")

	_, err = testTool(t, toolsdk.WorkspaceProcessOutput, deps, toolsdk.WorkspaceProcessOutputArgs{
		Workspace: "test-workspace",
	})
	require.ErrorContains(t, err, "process_id cannot be empty")

	_, err = testTool(t, toolsdk.WorkspaceProcessList, deps, toolsdk.WorkspaceProcessListArgs{})
	require.ErrorContains(t, err, "workspace name cannot be empty")

	_, err = testTool(t, toolsdk.WorkspaceProcessSignal, deps, toolsdk.WorkspaceProcessSignalArgs{
		Workspace: "test-workspace",
		ProcessID: "process-id",
		Signal:    "invalid",
	})
	require.ErrorContains(t, err, `signal must be "terminate" or "kill"`)
}

func TestWorkspaceProcessToolInstructions(t *testing.T) {
	t.Parallel()

	require.Contains(t, toolsdk.WorkspaceProcessStart.Description, "instead of coder_workspace_bash")
	require.Contains(t, toolsdk.WorkspaceProcessStart.Description, "does not wait for completion")
	require.Contains(t, toolsdk.WorkspaceProcessStart.Description, "DO NOT start the command again")
	require.Contains(t, toolsdk.WorkspaceProcessStart.Description, "coder_workspace_process_list")
	require.Contains(t, toolsdk.WorkspaceProcessStart.Description, "only /home/coder is persistent")
	require.Contains(t, toolsdk.WorkspaceProcessStart.Description, "sudo")
	require.Contains(t, toolsdk.WorkspaceProcessOutput.Description, "coder_workspace_process_start")
	require.Contains(t, toolsdk.WorkspaceProcessOutput.Description, "structured persistence advisory")
	require.Contains(t, toolsdk.WorkspaceProcessList.Description, "structured persistence advisory")

	// Keep the legacy bash execution contract, but make tool selection explicit.
	require.Contains(t, toolsdk.WorkspaceBash.Description, "short, ordinary commands")
	require.Contains(t, toolsdk.WorkspaceBash.Description, "coder_workspace_process_start")
	require.Contains(t, toolsdk.WorkspaceBash.Description, "non-idempotent")
	require.Contains(t, toolsdk.WorkspaceBash.Description, "only /home/coder is persistent")
	require.Contains(t, toolsdk.WorkspaceBash.Description, "stdout/stderr are not modified")
}

func TestWorkspaceProcessIntegration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("workspace Agent process execution uses sh -c")
	}

	client, workspace, agentToken := setupWorkspaceForAgent(t, nil)
	_ = agenttest.New(t, client.URL, agentToken)
	coderdtest.NewWorkspaceAgentWaiter(t, client, workspace.ID).Wait()

	deps, err := toolsdk.NewDeps(client)
	require.NoError(t, err)

	t.Run("StartListOutput", func(t *testing.T) {
		ctx := toolsdk.WithInvocationTool(t.Context(), "process_start")
		started, err := toolsdk.WorkspaceProcessStart.Handler(ctx, deps, toolsdk.WorkspaceProcessStartArgs{
			Workspace:  workspace.Name,
			Command:    `printf '%s:%s\n' "$PROCESS_TOOL_VALUE" "$PWD"; sleep 2; echo done`,
			WorkDir:    "/tmp",
			Env:        map[string]string{"PROCESS_TOOL_VALUE": "value"},
			Background: true,
		})
		require.NoError(t, err)
		require.True(t, started.Started)
		require.NotEmpty(t, started.ProcessID)

		listed, err := testTool(t, toolsdk.WorkspaceProcessList, deps, toolsdk.WorkspaceProcessListArgs{
			Workspace: workspace.Name,
		})
		require.NoError(t, err)

		var found bool
		for _, process := range listed.Processes {
			if process.ID != started.ProcessID {
				continue
			}
			found = true
			require.Equal(t, "/tmp", process.WorkDir)
			require.Equal(t, "process_start", process.Tool)
			require.True(t, process.Background)
			require.Contains(t, process.Command, "PROCESS_TOOL_VALUE")
			break
		}
		require.True(t, found, "started process must be recoverable with process_list")

		waitMs := 5000
		completed, err := testTool(t, toolsdk.WorkspaceProcessOutput, deps, toolsdk.WorkspaceProcessOutputArgs{
			Workspace:     workspace.Name,
			ProcessID:     started.ProcessID,
			WaitTimeoutMs: &waitMs,
		})
		require.NoError(t, err)
		require.False(t, completed.Running)
		require.Equal(t, 0, completed.ExitCode)
		require.Equal(t, started.ProcessID, completed.ProcessID)
		require.Contains(t, completed.Output, "value:/tmp")
		require.Contains(t, completed.Output, "done")
	})

	t.Run("CommandActivityToolAttribution", func(t *testing.T) {
		processCtx := toolsdk.WithInvocationTool(t.Context(), "process_start")
		started, err := toolsdk.WorkspaceProcessStart.Handler(processCtx, deps, toolsdk.WorkspaceProcessStartArgs{
			Workspace:  workspace.Name,
			Command:    `printf 'process-start-tool-marker\n'; sleep 5`,
			Background: true,
		})
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			activity, err := client.WorkspaceCommandActivity(t.Context(), workspace.ID)
			if err != nil {
				return false
			}
			for _, item := range activity.Activity {
				if item.Tool == "process_start" && item.Source == codersdk.WorkspaceCommandActivitySourceMCP && item.Status == codersdk.WorkspaceCommandActivityStatusRunning && strings.Contains(item.Command, "process-start-tool-marker") {
					return true
				}
			}
			return false
		}, 4*time.Second, 50*time.Millisecond, "running process_start command should be attributed in command activity")

		execCtx := toolsdk.WithInvocationTool(t.Context(), "exec")
		execResult, err := toolsdk.WorkspaceExec.Handler(execCtx, deps, toolsdk.WorkspaceExecArgs{
			Workspace: workspace.Name,
			Argv:      []string{"/bin/sh", "-c", `printf 'exec-tool-marker\n'`},
		})
		require.NoError(t, err)
		require.Equal(t, 0, execResult.ExitCode)

		bashCtx := toolsdk.WithInvocationTool(t.Context(), "bash")
		bashResult, err := toolsdk.WorkspaceBash.Handler(bashCtx, deps, toolsdk.WorkspaceBashArgs{
			Workspace: workspace.Name,
			Command:   `printf 'bash-tool-marker\n'`,
		})
		require.NoError(t, err)
		require.Equal(t, 0, bashResult.ExitCode)

		// Exercise the synthetic Idle timeline through the real HTTP+dbauthz path.
		// This protects the timeline-specific authorization wrappers from silently
		// regressing to generated not-implemented stubs.
		_, err = client.WorkspaceCommandActivityWithFilter(t.Context(), workspace.ID, codersdk.WorkspaceCommandActivityRequest{
			WorkspaceCommandActivityFilter: codersdk.WorkspaceCommandActivityFilter{
				Statuses: []codersdk.WorkspaceCommandActivityStatus{
					codersdk.WorkspaceCommandActivityStatusRunning,
					codersdk.WorkspaceCommandActivityStatusSucceeded,
					codersdk.WorkspaceCommandActivityStatusFailed,
					codersdk.WorkspaceCommandActivityStatusInterrupted,
					codersdk.WorkspaceCommandActivityStatusIdle,
				},
			},
		})
		require.NoError(t, err)

		connectionActivity, err := client.WorkspaceConnectionActivity(t.Context(), workspace.ID)
		require.NoError(t, err)
		for _, connection := range connectionActivity.Types {
			if connection.Type == codersdk.ConnectionTypeSSH {
				require.Zero(t, connection.ActiveConnections, "MCP bash must not create an SSH connection")
				require.Nil(t, connection.LastConnectedAt, "MCP bash must not update SSH last connected")
			}
		}

		waitMs := 10_000
		_, err = toolsdk.WorkspaceProcessOutput.Handler(t.Context(), deps, toolsdk.WorkspaceProcessOutputArgs{
			Workspace:     workspace.Name,
			ProcessID:     started.ProcessID,
			WaitTimeoutMs: &waitMs,
		})
		require.NoError(t, err)

		require.Eventually(t, func() bool {
			activity, err := client.WorkspaceCommandActivity(t.Context(), workspace.ID)
			if err != nil {
				return false
			}
			found := map[string]bool{}
			for _, item := range activity.Activity {
				if item.Status == codersdk.WorkspaceCommandActivityStatusRunning {
					continue
				}
				switch item.Tool {
				case "exec":
					found["exec"] = item.Source == codersdk.WorkspaceCommandActivitySourceMCP && strings.Contains(strings.Join(item.Argv, " "), "exec-tool-marker")
				case "bash":
					found["bash"] = item.Source == codersdk.WorkspaceCommandActivitySourceMCP && strings.Contains(item.Command, "bash-tool-marker")
				case "process_start":
					if strings.Contains(item.Command, "process-start-tool-marker") {
						found["process_start"] = item.Source == codersdk.WorkspaceCommandActivitySourceMCP
					}
				}
			}
			return found["exec"] && found["bash"] && found["process_start"]
		}, 5*time.Second, 50*time.Millisecond, "completed command history should expose the invoking MCP tool")
	})

	t.Run("CallerContextDoesNotOwnProcess", func(t *testing.T) {
		callerCtx, cancel := context.WithCancel(t.Context())
		started, err := toolsdk.WorkspaceProcessStart.Handler(callerCtx, deps, toolsdk.WorkspaceProcessStartArgs{
			Workspace: workspace.Name,
			Command:   `echo caller-start; sleep 2; echo caller-finished`,
		})
		require.NoError(t, err)
		require.True(t, started.Started)
		require.NotEmpty(t, started.ProcessID)

		cancel()

		waitMs := 5000
		completed, err := testTool(t, toolsdk.WorkspaceProcessOutput, deps, toolsdk.WorkspaceProcessOutputArgs{
			Workspace:     workspace.Name,
			ProcessID:     started.ProcessID,
			WaitTimeoutMs: &waitMs,
		})
		require.NoError(t, err)
		require.False(t, completed.Running)
		require.Equal(t, 0, completed.ExitCode)
		require.Contains(t, completed.Output, "caller-start")
		require.Contains(t, completed.Output, "caller-finished")
	})

	t.Run("Signal", func(t *testing.T) {
		started, err := testTool(t, toolsdk.WorkspaceProcessStart, deps, toolsdk.WorkspaceProcessStartArgs{
			Workspace: workspace.Name,
			Command:   `echo ready; sleep 30; echo unexpected`,
		})
		require.NoError(t, err)
		require.True(t, started.Started)

		signaled, err := testTool(t, toolsdk.WorkspaceProcessSignal, deps, toolsdk.WorkspaceProcessSignalArgs{
			Workspace: workspace.Name,
			ProcessID: started.ProcessID,
			Signal:    "terminate",
		})
		require.NoError(t, err)
		require.True(t, signaled.Success)

		waitMs := 5000
		stopped, err := testTool(t, toolsdk.WorkspaceProcessOutput, deps, toolsdk.WorkspaceProcessOutputArgs{
			Workspace:     workspace.Name,
			ProcessID:     started.ProcessID,
			WaitTimeoutMs: &waitMs,
		})
		require.NoError(t, err)
		require.False(t, stopped.Running)
		require.NotEqual(t, 0, stopped.ExitCode)
		require.Contains(t, stopped.Output, "ready")
		require.NotContains(t, stopped.Output, "unexpected")
	})
}
