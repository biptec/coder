package toolsdk_test

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/agent/agenttest"
	"github.com/coder/coder/v2/coderd/coderdtest"
	"github.com/coder/coder/v2/codersdk/toolsdk"
	"github.com/coder/coder/v2/testutil"
)

func TestWorkspaceBash(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: Workspace MCP bash tools rely on a Unix-like shell (bash) and POSIX process semantics. Use Linux/macOS or WSL for these tests.")
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
		t.Skip("Skipping on Windows: Workspace MCP bash tools rely on a Unix-like shell (bash) and POSIX process semantics. Use Linux/macOS or WSL for these tests.")
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

func TestWorkspaceProcessToolsValidateArgs(t *testing.T) {
	t.Parallel()

	deps := toolsdk.Deps{}

	_, err := testTool(t, toolsdk.WorkspaceProcessOutput, deps, toolsdk.WorkspaceProcessOutputArgs{
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

// Pure helper behavior for durable process execution is covered in
// process_internal_test.go. The integration tests below exercise the full
// Agent process API when the test environment provides its database runtime.

func TestWorkspaceBashTimeout(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: Workspace MCP bash tools rely on a Unix-like shell (bash) and POSIX process semantics. Use Linux/macOS or WSL for these tests.")
	}

	t.Run("TimeoutDefaultValue", func(t *testing.T) {
		t.Parallel()

		// Test that the TimeoutMs field can be set and read correctly
		args := toolsdk.WorkspaceBashArgs{
			TimeoutMs: 0, // Should default to 60000 in handler
		}

		// Verify that the TimeoutMs field exists and can be set
		require.Equal(t, 0, args.TimeoutMs)

		// Test setting a positive value
		args.TimeoutMs = 5000
		require.Equal(t, 5000, args.TimeoutMs)
	})

	t.Run("TimeoutNegativeValue", func(t *testing.T) {
		t.Parallel()

		// Direct struct construction can still contain a negative value. The handler
		// normalizes non-positive values to the default observation window; JSON tool
		// calls are additionally constrained by the schema minimum.
		args := toolsdk.WorkspaceBashArgs{TimeoutMs: -100}
		require.Equal(t, -100, args.TimeoutMs)
	})

	t.Run("TimeoutSchemaValidation", func(t *testing.T) {
		t.Parallel()

		tool := toolsdk.WorkspaceBash

		// Check that timeout_ms is in the schema
		require.Contains(t, tool.Schema.Properties, "timeout_ms")

		timeoutProperty := tool.Schema.Properties["timeout_ms"].(map[string]any)
		require.Equal(t, "integer", timeoutProperty["type"])
		require.Equal(t, 60000, timeoutProperty["default"])
		require.Equal(t, 1, timeoutProperty["minimum"])
		require.Equal(t, 60000, timeoutProperty["maximum"])
		require.Contains(t, timeoutProperty["description"], "observes the durable process")
		require.Contains(t, timeoutProperty["description"], "Maximum 60000ms")
	})

	t.Run("TimeoutDescriptionUpdated", func(t *testing.T) {
		t.Parallel()

		tool := toolsdk.WorkspaceBash

		// Check that description mentions timeout functionality
		require.Contains(t, tool.Description, "timeout_ms parameter")
		require.Contains(t, tool.Description, "defaults to 60000ms")
		require.Contains(t, tool.Description, "timeout_ms: 30000")
	})

	t.Run("TimeoutCommandScenario", func(t *testing.T) {
		t.Parallel()

		// Scenario: the command outlives the 5ms observation window. The durable
		// process must continue on the agent and be recovered by process_id.
		args := toolsdk.WorkspaceBashArgs{
			Workspace: "test-workspace",
			Command:   `echo "123"; sleep 60; echo "456"`, // This command would take 60+ seconds
			TimeoutMs: 5,                                  // 5ms timeout - should timeout after first echo
		}

		// Verify the args are structured correctly for the intended test scenario
		require.Equal(t, "test-workspace", args.Workspace)
		require.Contains(t, args.Command, `echo "123"`)
		require.Contains(t, args.Command, "sleep 60")
		require.Contains(t, args.Command, `echo "456"`)
		require.Equal(t, 5, args.TimeoutMs)

		// Note: The actual timeout behavior would need to be tested with a real workspace
		// This test just verifies the structure is correct for the timeout scenario
	})
}

func TestWorkspaceBashTimeoutIntegration(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: Workspace MCP bash tools rely on a Unix-like shell (bash) and POSIX process semantics. Use Linux/macOS or WSL for these tests.")
	}

	t.Run("ActualTimeoutBehavior", func(t *testing.T) {
		t.Parallel()

		// A command may outlive a single MCP request, but it must be started exactly
		// once and remain recoverable by its durable agent process ID.
		client, workspace, agentToken := setupWorkspaceForAgent(t, nil)
		_ = agenttest.New(t, client.URL, agentToken)
		coderdtest.NewWorkspaceAgentWaiter(t, client, workspace.ID).Wait()

		deps, err := toolsdk.NewDeps(client)
		require.NoError(t, err)

		result, err := testTool(t, toolsdk.WorkspaceBash, deps, toolsdk.WorkspaceBashArgs{
			Workspace: workspace.Name,
			Command:   `echo "123" && sleep 2 && echo "456"`,
			TimeoutMs: 100,
		})
		require.NoError(t, err)
		require.Equal(t, 124, result.ExitCode)
		require.True(t, result.Running)
		require.NotEmpty(t, result.ProcessID)
		require.Contains(t, result.Output, "123")
		require.NotContains(t, result.Output, "456")

		// Recovery does not depend on the caller remembering the ID: process_list can
		// rediscover it after a transport disconnect before anything is rerun.
		listed, err := testTool(t, toolsdk.WorkspaceProcessList, deps, toolsdk.WorkspaceProcessListArgs{
			Workspace: workspace.Name,
		})
		require.NoError(t, err)
		found := false
		for _, process := range listed.Processes {
			if process.ID == result.ProcessID {
				found = true
				require.True(t, process.Running)
				break
			}
		}
		require.True(t, found, "timed-out command must remain discoverable")

		waitMs := 5000
		completed, err := testTool(t, toolsdk.WorkspaceProcessOutput, deps, toolsdk.WorkspaceProcessOutputArgs{
			Workspace:     workspace.Name,
			ProcessID:     result.ProcessID,
			WaitTimeoutMs: &waitMs,
		})
		require.NoError(t, err)
		require.False(t, completed.Running)
		require.Equal(t, 0, completed.ExitCode)
		require.Equal(t, result.ProcessID, completed.ProcessID)
		require.Contains(t, completed.Output, "123")
		require.Contains(t, completed.Output, "456")
	})

	t.Run("CallerCancellationDoesNotKillProcess", func(t *testing.T) {
		t.Parallel()

		client, workspace, agentToken := setupWorkspaceForAgent(t, nil)
		_ = agenttest.New(t, client.URL, agentToken)
		coderdtest.NewWorkspaceAgentWaiter(t, client, workspace.ID).Wait()

		deps, err := toolsdk.NewDeps(client)
		require.NoError(t, err)

		callerCtx, cancelCaller := context.WithCancel(t.Context())
		defer cancelCaller()

		type bashCallResult struct {
			err error
		}
		callDone := make(chan bashCallResult, 1)
		go func() {
			_, err := toolsdk.WorkspaceBash.Handler(callerCtx, deps, toolsdk.WorkspaceBashArgs{
				Workspace: workspace.Name,
				Command:   `echo "caller-cancel-start" && sleep 3 && echo "caller-cancel-finished"`,
				TimeoutMs: 60000,
			})
			callDone <- bashCallResult{err: err}
		}()

		var processID string
		require.Eventually(t, func() bool {
			listed, err := testTool(t, toolsdk.WorkspaceProcessList, deps, toolsdk.WorkspaceProcessListArgs{
				Workspace: workspace.Name,
			})
			if err != nil {
				return false
			}
			for _, process := range listed.Processes {
				if process.Running && strings.Contains(process.Command, "caller-cancel-start") {
					processID = process.ID
					return true
				}
			}
			return false
		}, testutil.WaitMedium, testutil.IntervalMedium, "started process should become discoverable before caller cancellation")

		cancelCaller()

		var canceled bashCallResult
		require.Eventually(t, func() bool {
			select {
			case canceled = <-callDone:
				return true
			default:
				return false
			}
		}, testutil.WaitMedium, testutil.IntervalMedium, "canceled caller should return without waiting for process exit")
		require.Error(t, canceled.err)
		require.ErrorContains(t, canceled.err, "context canceled")

		waitMs := 5000
		recovered, err := testTool(t, toolsdk.WorkspaceProcessOutput, deps, toolsdk.WorkspaceProcessOutputArgs{
			Workspace:     workspace.Name,
			ProcessID:     processID,
			WaitTimeoutMs: &waitMs,
		})
		require.NoError(t, err)
		require.False(t, recovered.Running)
		require.Equal(t, 0, recovered.ExitCode)
		require.Equal(t, processID, recovered.ProcessID)
		require.Contains(t, recovered.Output, "caller-cancel-start")
		require.Contains(t, recovered.Output, "caller-cancel-finished")
	})

	t.Run("NormalCommandExecution", func(t *testing.T) {
		t.Parallel()

		// Test that normal commands still work with timeout functionality present

		client, workspace, agentToken := setupWorkspaceForAgent(t, nil)

		// Start the agent and wait for it to be fully ready
		_ = agenttest.New(t, client.URL, agentToken)

		// Wait for workspace agents to be ready
		coderdtest.NewWorkspaceAgentWaiter(t, client, workspace.ID).Wait()

		deps, err := toolsdk.NewDeps(client)
		require.NoError(t, err)

		args := toolsdk.WorkspaceBashArgs{
			Workspace: workspace.Name,
			Command:   `echo "normal command"`, // Quick command that should complete normally
			TimeoutMs: 5000,                    // 5 second timeout - plenty of time
		}

		// Use testTool to register the tool as tested and satisfy coverage validation
		result, err := testTool(t, toolsdk.WorkspaceBash, deps, args)

		// Should not error
		require.NoError(t, err)

		t.Logf("result.Output: %s", result.Output)

		// Should have exit code 0 (success)
		require.Equal(t, 0, result.ExitCode)

		// Should contain the expected output
		require.Equal(t, "normal command", result.Output)

		// Should NOT contain timeout message
		require.NotContains(t, result.Output, "Command canceled due to timeout")
	})

	t.Run("TrackedProcessSignal", func(t *testing.T) {
		t.Parallel()

		client, workspace, agentToken := setupWorkspaceForAgent(t, nil)
		_ = agenttest.New(t, client.URL, agentToken)
		coderdtest.NewWorkspaceAgentWaiter(t, client, workspace.ID).Wait()

		deps, err := toolsdk.NewDeps(client)
		require.NoError(t, err)

		result, err := testTool(t, toolsdk.WorkspaceBash, deps, toolsdk.WorkspaceBashArgs{
			Workspace: workspace.Name,
			Command:   `echo "ready" && sleep 30 && echo "unexpected"`,
			TimeoutMs: 100,
		})
		require.NoError(t, err)
		require.True(t, result.Running)
		require.NotEmpty(t, result.ProcessID)

		signaled, err := testTool(t, toolsdk.WorkspaceProcessSignal, deps, toolsdk.WorkspaceProcessSignalArgs{
			Workspace: workspace.Name,
			ProcessID: result.ProcessID,
			Signal:    "terminate",
		})
		require.NoError(t, err)
		require.True(t, signaled.Success)

		waitMs := 5000
		stopped, err := testTool(t, toolsdk.WorkspaceProcessOutput, deps, toolsdk.WorkspaceProcessOutputArgs{
			Workspace:     workspace.Name,
			ProcessID:     result.ProcessID,
			WaitTimeoutMs: &waitMs,
		})
		require.NoError(t, err)
		require.False(t, stopped.Running)
		require.NotEqual(t, 0, stopped.ExitCode)
		require.Contains(t, stopped.Output, "ready")
		require.NotContains(t, stopped.Output, "unexpected")
	})
}

func TestWorkspaceBashBackgroundIntegration(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Skipping on Windows: Workspace MCP bash tools rely on a Unix-like shell (bash) and POSIX process semantics. Use Linux/macOS or WSL for these tests.")
	}

	t.Run("BackgroundCommandCapturesOutput", func(t *testing.T) {
		t.Parallel()

		client, workspace, agentToken := setupWorkspaceForAgent(t, nil)

		// Start the agent and wait for it to be fully ready
		_ = agenttest.New(t, client.URL, agentToken)

		// Wait for workspace agents to be ready
		coderdtest.NewWorkspaceAgentWaiter(t, client, workspace.ID).Wait()

		deps, err := toolsdk.NewDeps(client)
		require.NoError(t, err)

		args := toolsdk.WorkspaceBashArgs{
			Workspace:  workspace.Name,
			Command:    `echo "started" && sleep 60 && echo "completed"`, // Command that would take 60+ seconds
			Background: true,                                             // Run in background
			TimeoutMs:  2000,                                             // 2 second timeout
		}

		result, err := testTool(t, toolsdk.WorkspaceBash, deps, args)

		// Should not error
		require.NoError(t, err)

		t.Logf("Background result: exitCode=%d, output=%q", result.ExitCode, result.Output)

		// Should have exit code 124 (timeout) since command times out
		require.Equal(t, 124, result.ExitCode)

		// Should capture output up to timeout point
		require.Contains(t, result.Output, "started", "Should contain output captured before timeout")

		// Should NOT contain the second echo (it never executed due to timeout)
		require.NotContains(t, result.Output, "completed", "Should not contain output after timeout")

		// Should contain background continuation message
		require.Contains(t, result.Output, "Command continues running in background")
	})

	t.Run("BackgroundVsNormalExecution", func(t *testing.T) {
		t.Parallel()

		client, workspace, agentToken := setupWorkspaceForAgent(t, nil)

		// Start the agent and wait for it to be fully ready
		_ = agenttest.New(t, client.URL, agentToken)

		// Wait for workspace agents to be ready
		coderdtest.NewWorkspaceAgentWaiter(t, client, workspace.ID).Wait()

		deps, err := toolsdk.NewDeps(client)
		require.NoError(t, err)

		// First run the same command in normal mode
		normalArgs := toolsdk.WorkspaceBashArgs{
			Workspace:  workspace.Name,
			Command:    `echo "hello world"`,
			Background: false,
		}

		normalResult, err := toolsdk.WorkspaceBash.Handler(t.Context(), deps, normalArgs)
		require.NoError(t, err)

		// Normal mode should return the actual output
		require.Equal(t, 0, normalResult.ExitCode)
		require.Equal(t, "hello world", normalResult.Output)

		// Now run the same command in background mode
		backgroundArgs := toolsdk.WorkspaceBashArgs{
			Workspace:  workspace.Name,
			Command:    `echo "hello world"`,
			Background: true,
		}

		backgroundResult, err := testTool(t, toolsdk.WorkspaceBash, deps, backgroundArgs)
		require.NoError(t, err)

		t.Logf("Normal result: %q", normalResult.Output)
		t.Logf("Background result: %q", backgroundResult.Output)

		// Background mode should also return the actual output since command completes quickly
		require.Equal(t, 0, backgroundResult.ExitCode)
		require.Equal(t, "hello world", backgroundResult.Output)
	})

	t.Run("BackgroundCommandContinuesAfterTimeout", func(t *testing.T) {
		t.Parallel()

		client, workspace, agentToken := setupWorkspaceForAgent(t, nil)

		// Start the agent and wait for it to be fully ready
		_ = agenttest.New(t, client.URL, agentToken)

		// Wait for workspace agents to be ready
		coderdtest.NewWorkspaceAgentWaiter(t, client, workspace.ID).Wait()

		deps, err := toolsdk.NewDeps(client)
		require.NoError(t, err)

		args := toolsdk.WorkspaceBashArgs{
			Workspace:  workspace.Name,
			Command:    `echo "started" && sleep 4 && echo "done" > /tmp/bg-test-done`, // Command that will timeout but continue
			TimeoutMs:  2000,                                                           // 2000ms timeout (shorter than command duration)
			Background: true,                                                           // Run in background
		}

		result, err := testTool(t, toolsdk.WorkspaceBash, deps, args)

		// Should not error but should timeout
		require.NoError(t, err)

		t.Logf("Background with timeout result: exitCode=%d, output=%q", result.ExitCode, result.Output)

		// Should have timeout exit code
		require.Equal(t, 124, result.ExitCode)

		// Should capture output before timeout
		require.Contains(t, result.Output, "started", "Should contain output captured before timeout")

		// Should contain background continuation message
		require.Contains(t, result.Output, "Command continues running in background")

		// Wait for the durable background process to complete after this observation returned.
		require.Eventually(t, func() bool {
			checkArgs := toolsdk.WorkspaceBashArgs{
				Workspace: workspace.Name,
				Command:   `cat /tmp/bg-test-done 2>/dev/null || echo "not found"`,
			}
			checkResult, err := toolsdk.WorkspaceBash.Handler(t.Context(), deps, checkArgs)
			return err == nil && checkResult.Output == "done"
		}, testutil.WaitMedium, testutil.IntervalMedium, "Background command should continue running and complete after timeout")
	})
}
