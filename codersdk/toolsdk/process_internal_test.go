package toolsdk

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func TestWorkspaceProcessWaitDuration(t *testing.T) {
	t.Parallel()

	maxWait := codersdk.DefaultMCPToolTimeoutMax

	wait, err := workspaceProcessWaitDuration(nil, maxWait)
	require.NoError(t, err)
	require.Zero(t, wait, "process_output without wait_timeout_ms must be an immediate snapshot")

	zero := 0
	wait, err = workspaceProcessWaitDuration(&zero, maxWait)
	require.NoError(t, err)
	require.Zero(t, wait)

	maxWaitMs := int(maxWait.Milliseconds())
	wait, err = workspaceProcessWaitDuration(&maxWaitMs, maxWait)
	require.NoError(t, err)
	require.Equal(t, maxWait, wait)

	negative := -1
	_, err = workspaceProcessWaitDuration(&negative, maxWait)
	require.ErrorContains(t, err, "cannot be negative")

	tooLarge := maxWaitMs + 1
	_, err = workspaceProcessWaitDuration(&tooLarge, maxWait)
	require.ErrorContains(t, err, "cannot exceed")
}

func TestWorkspaceExecutionWaitDuration(t *testing.T) {
	t.Parallel()

	maxWait := codersdk.DefaultMCPToolTimeoutMax
	wait, err := workspaceExecutionWaitDuration(nil, maxWait)
	require.NoError(t, err)
	require.Equal(t, maxWait, wait, "exec/bash without wait_timeout_ms must wait up to the global maximum")

	explicit := 2500
	wait, err = workspaceExecutionWaitDuration(&explicit, maxWait)
	require.NoError(t, err)
	require.Equal(t, 2500*time.Millisecond, wait)
}

func TestWorkspaceProcessWaitWithinBudget(t *testing.T) {
	t.Parallel()

	full := mcpObservationBudget{deadline: time.Now().Add(30 * time.Second)}
	require.LessOrEqual(t, workspaceProcessWaitWithinBudget(60*time.Second, full), 30*time.Second)
	require.Greater(t, workspaceProcessWaitWithinBudget(60*time.Second, full), 29*time.Second)

	short := mcpObservationBudget{deadline: time.Now().Add(4 * time.Second)}
	require.LessOrEqual(t, workspaceProcessWaitWithinBudget(10*time.Second, short), 4*time.Second)
	require.Greater(t, workspaceProcessWaitWithinBudget(10*time.Second, short), 3*time.Second)
}

func TestMCPObservationBudgetUsesDeploymentMaximum(t *testing.T) {
	t.Parallel()

	deps := Deps{}
	budget := newMCPObservationBudget(deps)
	require.Equal(t, codersdk.DefaultMCPToolTimeoutMax, budget.max)
	require.Equal(t, codersdk.DefaultMCPToolTimeoutMax-processSnapshotTimeout, budget.window)
	require.Equal(t, 5*time.Second, processSnapshotTimeout)
}

func TestWorkspaceProcessResult(t *testing.T) {
	t.Parallel()

	truncation := &workspacesdk.ProcessTruncation{
		OriginalBytes: 100,
		RetainedBytes: 80,
		OmittedBytes:  20,
		Strategy:      "head-tail",
	}

	running := workspaceProcessResult("process-running", workspacesdk.ProcessOutputResponse{
		Output:    "  partial output \n",
		Running:   true,
		Truncated: truncation,
	})
	require.Equal(t, "partial output", running.Output)
	require.Equal(t, 124, running.ExitCode)
	require.Equal(t, "process-running", running.ProcessID)
	require.True(t, running.Running)
	require.Equal(t, truncation, running.Truncated)

	exitCode := 7
	completed := workspaceProcessResult("process-completed", workspacesdk.ProcessOutputResponse{
		Output:   "\n done \n",
		Running:  false,
		ExitCode: &exitCode,
	})
	require.Equal(t, "done", completed.Output)
	require.Equal(t, 7, completed.ExitCode)
	require.Equal(t, "process-completed", completed.ProcessID)
	require.False(t, completed.Running)
	require.Nil(t, completed.Truncated)
}

func TestProcessToolsRegistered(t *testing.T) {
	t.Parallel()

	found := make(map[string]bool)
	for _, tool := range All {
		found[tool.Name] = true
	}

	for _, name := range []string{
		ToolNameWorkspaceProcessStart,
		ToolNameWorkspaceProcessOutput,
		ToolNameWorkspaceProcessList,
		ToolNameWorkspaceProcessSignal,
	} {
		require.True(t, found[name], "tool %q must be registered", name)
	}
}
