package toolsdk_test

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/agent/agenttest"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/toolsdk"
	"github.com/coder/coder/v2/testutil"
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

	_, err = testTool(t, toolsdk.WorkspaceListSystemProcesses, deps, toolsdk.WorkspaceListSystemProcessesArgs{
		Workspace: "test-workspace",
		Limit:     -1,
	})
	require.ErrorContains(t, err, "limit cannot be negative")

	_, err = testTool(t, toolsdk.WorkspaceProcessSignal, deps, toolsdk.WorkspaceProcessSignalArgs{
		Workspace: "test-workspace",
		ProcessID: "process-id",
		Signal:    "invalid",
	})
	require.ErrorContains(t, err, `signal must be "interrupt", "terminate", or "kill"`)
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

	require.Contains(t, toolsdk.WorkspaceProcessStartV2.Description, "wait_timeout_ms")
	require.Contains(t, toolsdk.WorkspaceProcessStartV2.Description, "Observation time never")
	require.Contains(t, toolsdk.WorkspaceProcessStartV2.Description, "initial output")
	require.Contains(t, toolsdk.WorkspaceProcessStartV2.Description, "list_sessions")
	require.Contains(t, toolsdk.WorkspaceProcessOutput.Description, "deployment-wide MCP tool timeout")
	require.Contains(t, toolsdk.WorkspaceProcessOutput.Description, "immediate snapshot")
	require.Contains(t, toolsdk.WorkspaceProcessOutput.Description, "never terminates the durable process")

	// The shell tool is the explicit sh -c counterpart to argv-only start_process.
	require.Contains(t, toolsdk.WorkspaceBash.Description, "POSIX shell command")
	require.Contains(t, toolsdk.WorkspaceBash.Description, "sh -c")
	require.Contains(t, toolsdk.WorkspaceBash.Description, "start_process")
	require.Contains(t, toolsdk.WorkspaceBash.Description, "wait_timeout_ms")
	require.Contains(t, toolsdk.WorkspaceBash.Description, "process_id")
}

//nolint:paralleltest // subtests intentionally share one workspace and agent and must run serially.
func TestWorkspaceProcessIntegration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("workspace Agent process execution uses sh -c")
	}

	client, workspace, agentToken := setupWorkspaceForAgent(t, nil)
	_ = agenttest.New(t, client.URL, agentToken)
	coderdtest.NewWorkspaceAgentWaiter(t, client, workspace.ID).Wait()

	deps, err := toolsdk.NewDeps(client)
	require.NoError(t, err)

	//nolint:paralleltest // shares the integration workspace and agent with sibling subtests.
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
			require.Equal(t, "start_process", process.Tool)
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
		require.NotNil(t, completed.ExitCode)
		require.Equal(t, 0, *completed.ExitCode)
		require.Equal(t, started.ProcessID, completed.ProcessID)
		require.Contains(t, completed.Output, "value:/tmp")
		require.Contains(t, completed.Output, "done")
	})

	//nolint:paralleltest // shares the integration workspace and agent with sibling subtests.
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
				if item.Tool == "start_process" && item.Source == codersdk.WorkspaceCommandActivitySourceMCP && item.Status == codersdk.WorkspaceCommandActivityStatusRunning && strings.Contains(item.Command, "process-start-tool-marker") {
					return true
				}
			}
			return false
		}, testutil.WaitShort, testutil.IntervalFast, "running start_process command should be attributed in command activity")

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
		require.NotNil(t, bashResult.ExitCode)
		require.Equal(t, 0, *bashResult.ExitCode)

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
				case "execute_shell_command":
					found["execute_shell_command"] = item.Source == codersdk.WorkspaceCommandActivitySourceMCP && strings.Contains(item.Command, "bash-tool-marker")
				case "start_process":
					if strings.Contains(item.Command, "process-start-tool-marker") {
						found["start_process"] = item.Source == codersdk.WorkspaceCommandActivitySourceMCP
					}
				}
			}
			return found["exec"] && found["execute_shell_command"] && found["start_process"]
		}, testutil.WaitShort, testutil.IntervalFast, "completed command history should expose the invoking MCP tool")
	})

	//nolint:paralleltest // shares the integration workspace and agent with sibling subtests.
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
		require.NotNil(t, completed.ExitCode)
		require.Equal(t, 0, *completed.ExitCode)
		require.Contains(t, completed.Output, "caller-start")
		require.Contains(t, completed.Output, "caller-finished")
	})

	//nolint:paralleltest // shares the integration workspace and agent with sibling subtests.
	t.Run("ListSystemProcesses", func(t *testing.T) {
		started, err := testTool(t, toolsdk.WorkspaceProcessStartV2, deps, toolsdk.WorkspaceProcessStartV2Args{
			Workspace: workspace.Name,
			Argv:      []string{"sleep", "30"},
		})
		require.NoError(t, err)
		require.True(t, started.Running)
		require.NotEmpty(t, started.ProcessID)

		require.Eventually(t, func() bool {
			listed, err := testTool(t, toolsdk.WorkspaceListSystemProcesses, deps, toolsdk.WorkspaceListSystemProcessesArgs{
				Workspace: workspace.Name,
				Filter:    "sleep 30",
				Limit:     20,
			})
			if err != nil {
				return false
			}
			for _, process := range listed.Processes {
				if process.PID > 0 && strings.Contains(process.Command, "sleep 30") {
					return true
				}
			}
			return false
		}, testutil.WaitShort, testutil.IntervalFast, "OS process listing should expose the running sleep process")

		_, err = testTool(t, toolsdk.WorkspaceProcessSignal, deps, toolsdk.WorkspaceProcessSignalArgs{
			Workspace: workspace.Name,
			ProcessID: started.ProcessID,
			Signal:    "terminate",
		})
		require.NoError(t, err)
	})

	//nolint:paralleltest // shares the integration workspace and agent with sibling subtests.
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
		require.NotNil(t, stopped.ExitCode)
		require.NotEqual(t, 0, *stopped.ExitCode)
		require.Contains(t, stopped.Output, "ready")
		require.NotContains(t, stopped.Output, "unexpected")
	})
}
