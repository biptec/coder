package toolsdk

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func TestWorkspaceBashCommand(t *testing.T) {
	t.Parallel()

	require.Equal(t, "exec bash -lc 'echo hi'", workspaceBashCommand("echo hi"))
	require.Equal(t, "'a'\"'\"'b'", shellSingleQuote("a'b"))
	require.Equal(
		t,
		"exec bash -lc 'printf '\"'\"'%s\\n'\"'\"' \"hello world\"'",
		workspaceBashCommand(`printf '%s\n' "hello world"`),
	)
}

func TestWorkspaceProcessWaitDuration(t *testing.T) {
	t.Parallel()

	wait, err := workspaceProcessWaitDuration(nil)
	require.NoError(t, err)
	require.Equal(t, defaultWorkspaceProcessWait, wait)

	zero := 0
	wait, err = workspaceProcessWaitDuration(&zero)
	require.NoError(t, err)
	require.Zero(t, wait)

	max := int(maxWorkspaceProcessWait.Milliseconds())
	wait, err = workspaceProcessWaitDuration(&max)
	require.NoError(t, err)
	require.Equal(t, maxWorkspaceProcessWait, wait)

	negative := -1
	_, err = workspaceProcessWaitDuration(&negative)
	require.ErrorContains(t, err, "cannot be negative")

	tooLarge := max + 1
	_, err = workspaceProcessWaitDuration(&tooLarge)
	require.ErrorContains(t, err, "cannot exceed")
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
		ToolNameWorkspaceBash,
		ToolNameWorkspaceProcessOutput,
		ToolNameWorkspaceProcessList,
		ToolNameWorkspaceProcessSignal,
	} {
		require.True(t, found[name], "tool %q must be registered", name)
	}
}

func TestWorkspaceProcessConstants(t *testing.T) {
	t.Parallel()

	require.Equal(t, 10*time.Second, defaultWorkspaceProcessWait)
	require.Equal(t, 60*time.Second, maxWorkspaceProcessWait)
	require.Equal(t, 5*time.Second, processSnapshotTimeout)
}
