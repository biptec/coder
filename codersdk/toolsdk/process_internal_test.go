package toolsdk

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
)

func TestWorkspaceProcessOutputWaitSchemaMatchesRuntime(t *testing.T) {
	t.Parallel()

	property, ok := WorkspaceProcessOutput.Schema.Properties["wait_timeout_ms"].(map[string]any)
	require.True(t, ok)
	require.EqualValues(t, 0, property["minimum"])
	require.NotContains(t, property, "default", "omitting wait_timeout_ms is an immediate snapshot")
	require.NotContains(t, property, "maximum", "maximum is deployment-specific")
	require.Contains(t, property["description"], "immediate snapshot")
	require.Contains(t, property["description"], "deployment-wide MCP tool timeout")
}

func TestWorkspaceProcessWaitDuration(t *testing.T) {
	t.Parallel()

	maxTimeout := 90 * time.Second
	wait, err := workspaceProcessWaitDuration(nil, maxTimeout)
	require.NoError(t, err)
	require.Zero(t, wait, "omitted wait_timeout_ms must be an immediate snapshot")

	zero := 0
	wait, err = workspaceProcessWaitDuration(&zero, maxTimeout)
	require.NoError(t, err)
	require.Zero(t, wait)

	maxWaitMs := int(maxTimeout.Milliseconds())
	wait, err = workspaceProcessWaitDuration(&maxWaitMs, maxTimeout)
	require.NoError(t, err)
	require.Equal(t, maxTimeout, wait)

	negative := -1
	_, err = workspaceProcessWaitDuration(&negative, maxTimeout)
	require.ErrorContains(t, err, "cannot be negative")

	tooLarge := maxWaitMs + 1
	_, err = workspaceProcessWaitDuration(&tooLarge, maxTimeout)
	require.ErrorContains(t, err, "cannot exceed deployment MCP tool timeout")
}

func TestMCPObservationBudgetUsesDeploymentTimeout(t *testing.T) {
	t.Parallel()

	defaultBudget := newMCPObservationBudget(Deps{})
	require.Equal(t, codersdk.DefaultMCPToolTimeoutMax, defaultBudget.max)
	require.Equal(t, codersdk.DefaultMCPToolTimeoutMax, defaultBudget.window)

	custom := newMCPObservationBudget(Deps{mcpToolTimeoutMax: 30 * time.Second})
	require.Equal(t, 30*time.Second, custom.max)
	require.Equal(t, 30*time.Second, custom.window)
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

func TestObserveInitialWorkspaceProcessWaitsAcrossOutputAndReturnsCursorSnapshot(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	exitCode := 0
	processID := "process-initial"

	first := conn.EXPECT().ProcessOutput(gomock.Any(), processID, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, opts *workspacesdk.ProcessOutputOptions) (workspacesdk.ProcessOutputResponse, error) {
			require.True(t, opts.Wait)
			require.Nil(t, opts.Cursor)
			return workspacesdk.ProcessOutputResponse{Running: true, Output: `first
`}, nil
		},
	)
	second := conn.EXPECT().ProcessOutput(gomock.Any(), processID, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, opts *workspacesdk.ProcessOutputOptions) (workspacesdk.ProcessOutputResponse, error) {
			require.True(t, opts.Wait)
			require.Nil(t, opts.Cursor)
			return workspacesdk.ProcessOutputResponse{Running: false, ExitCode: &exitCode, Output: `first
second
`}, nil
		},
	).After(first)
	conn.EXPECT().ProcessOutput(gomock.Any(), processID, gomock.Any()).DoAndReturn(
		func(_ context.Context, _ string, opts *workspacesdk.ProcessOutputOptions) (workspacesdk.ProcessOutputResponse, error) {
			require.False(t, opts.Wait)
			require.NotNil(t, opts.Cursor)
			require.Zero(t, *opts.Cursor)
			require.Zero(t, opts.Limit, "omitted limit must request all currently retained output")
			return workspacesdk.ProcessOutputResponse{
				Running:  false,
				ExitCode: &exitCode,
				Output: `first
second
`,
				NextCursor: 13,
			}, nil
		},
	).After(second)

	budget := mcpObservationBudget{deadline: time.Now().Add(10 * time.Second)}
	resp, err := observeInitialWorkspaceProcess(t.Context(), conn, processID, time.Second, budget)
	require.NoError(t, err)
	require.False(t, resp.Running)
	require.NotNil(t, resp.ExitCode)
	require.Equal(t, 0, *resp.ExitCode)
	require.Equal(t, `first
second
`, resp.Output)
	require.Equal(t, int64(13), resp.NextCursor)
}

func TestInteractWithWorkspaceProcessReturnsSnapshot(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	args := WorkspaceProcessInputArgs{
		ProcessID: "process-1",
		Data:      "hello\n",
		Limit:     1024,
	}

	gomock.InOrder(
		conn.EXPECT().ProcessOutput(gomock.Any(), "process-1", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, opts *workspacesdk.ProcessOutputOptions) (workspacesdk.ProcessOutputResponse, error) {
				require.False(t, opts.Wait)
				require.NotNil(t, opts.Cursor)
				require.Equal(t, int64(9223372036854775807), *opts.Cursor)
				require.Equal(t, 1, opts.Limit)
				return workspacesdk.ProcessOutputResponse{NextCursor: 4, Running: true}, nil
			},
		),
		conn.EXPECT().ProcessInput(gomock.Any(), "process-1", workspacesdk.ProcessInputRequest{
			Data: "hello\n",
		}),
		conn.EXPECT().ProcessOutput(gomock.Any(), "process-1", gomock.Any()).DoAndReturn(
			func(_ context.Context, _ string, opts *workspacesdk.ProcessOutputOptions) (workspacesdk.ProcessOutputResponse, error) {
				require.False(t, opts.Wait)
				require.NotNil(t, opts.Cursor)
				require.Equal(t, int64(4), *opts.Cursor)
				require.Equal(t, 1024, opts.Limit)
				return workspacesdk.ProcessOutputResponse{
					Output:     "reply\n",
					Running:    true,
					NextCursor: 10,
				}, nil
			},
		),
	)
	conn.EXPECT().ListProcesses(gomock.Any()).Return(workspacesdk.ListProcessesResponse{}, nil)

	budget := mcpObservationBudget{
		deadline: time.Now().Add(time.Second),
		window:   time.Second,
		max:      time.Second,
	}
	result, err := interactWithWorkspaceProcess(t.Context(), conn, args, 0, budget)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Empty(t, result.OutputError)
	require.NotNil(t, result.Process)
	require.Equal(t, "process-1", result.Process.ProcessID)
	require.Equal(t, "reply\n", result.Process.Output)
	require.True(t, result.Process.Running)
	require.NotNil(t, result.Process.NextCursor)
	require.Equal(t, int64(10), *result.Process.NextCursor)
}

func TestInteractWithWorkspaceProcessDoesNotFailAfterInputAccepted(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	args := WorkspaceProcessInputArgs{
		ProcessID: "process-2",
		Data:      "non-idempotent input",
	}

	gomock.InOrder(
		conn.EXPECT().ProcessOutput(gomock.Any(), "process-2", gomock.Any()).Return(
			workspacesdk.ProcessOutputResponse{NextCursor: 12, Running: true}, nil,
		),
		conn.EXPECT().ProcessInput(gomock.Any(), "process-2", workspacesdk.ProcessInputRequest{
			Data: "non-idempotent input",
		}),
		conn.EXPECT().ProcessOutput(gomock.Any(), "process-2", gomock.Any()).Return(
			workspacesdk.ProcessOutputResponse{},
			xerrors.New("temporary output failure"),
		),
	)

	budget := mcpObservationBudget{
		deadline: time.Now().Add(time.Second),
		window:   time.Second,
		max:      time.Second,
	}
	result, err := interactWithWorkspaceProcess(t.Context(), conn, args, 0, budget)
	require.NoError(t, err, "accepted stdin must not become a retryable top-level error")
	require.True(t, result.Success)
	require.Nil(t, result.Process)
	require.ErrorContains(t, xerrors.New(result.OutputError), "temporary output failure")
}

func TestInteractWithWorkspaceProcessInputFailureIsError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	args := WorkspaceProcessInputArgs{
		ProcessID: "process-3",
		Data:      "input",
	}

	gomock.InOrder(
		conn.EXPECT().ProcessOutput(gomock.Any(), "process-3", gomock.Any()).Return(
			workspacesdk.ProcessOutputResponse{NextCursor: 0, Running: true}, nil,
		),
		conn.EXPECT().ProcessInput(gomock.Any(), "process-3", workspacesdk.ProcessInputRequest{
			Data: "input",
		}).Return(xerrors.New("stdin closed")),
	)

	budget := mcpObservationBudget{
		deadline: time.Now().Add(time.Second),
		window:   time.Second,
		max:      time.Second,
	}
	result, err := interactWithWorkspaceProcess(t.Context(), conn, args, 0, budget)
	require.ErrorContains(t, err, "stdin closed")
	require.False(t, result.Success)
}

func TestSignalWorkspaceProcessReportsAlreadyCompleted(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	conflict := codersdk.ReadBodyAsError(&http.Response{
		StatusCode: http.StatusConflict,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"message":"Process is not running."}`)),
	})
	gomock.InOrder(
		conn.EXPECT().SignalProcess(gomock.Any(), "process-1", "terminate").Return(conflict),
		conn.EXPECT().ListProcesses(gomock.Any()).Return(workspacesdk.ListProcessesResponse{
			Processes: []workspacesdk.ProcessInfo{{ID: "process-1", Running: false}},
		}, nil),
	)

	result, err := signalWorkspaceProcess(t.Context(), conn, "process-1", "terminate")
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, "Process process-1 is already completed.", result.Message)
}

func TestSignalWorkspaceProcessPreservesUnknownFailure(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	conn.EXPECT().SignalProcess(gomock.Any(), "missing", "terminate").Return(xerrors.New("process not found"))

	result, err := signalWorkspaceProcess(t.Context(), conn, "missing", "terminate")
	require.ErrorContains(t, err, "process not found")
	require.False(t, result.Success)
}

func TestWorkspaceProcessListSchema(t *testing.T) {
	t.Parallel()

	require.ElementsMatch(t, []string{"workspace"}, WorkspaceProcessList.Schema.Required)
	limit := WorkspaceProcessList.Schema.Properties["limit"].(map[string]any)
	require.Contains(t, limit["description"], "If omitted")
	require.EqualValues(t, 1, limit["minimum"])
	require.NotContains(t, limit, "maximum")
	cursor := WorkspaceProcessList.Schema.Properties["cursor"].(map[string]any)
	require.Equal(t, "string", cursor["type"])
}

func TestWorkspaceProcessInputSchema(t *testing.T) {
	t.Parallel()

	require.ElementsMatch(t, []string{"workspace", "process_id"}, WorkspaceProcessInput.Schema.Required)
	for _, name := range []string{"data", "close", "wait_timeout_ms", "limit"} {
		require.Contains(t, WorkspaceProcessInput.Schema.Properties, name)
	}
	require.NotContains(t, WorkspaceProcessInput.Schema.Properties, "cursor")
	wait := WorkspaceProcessInput.Schema.Properties["wait_timeout_ms"].(map[string]any)
	require.Contains(t, wait["description"], "Omit or use 0")
	require.NotContains(t, wait, "maximum")
	limit := WorkspaceProcessInput.Schema.Properties["limit"].(map[string]any)
	require.NotContains(t, limit, "maximum")
	data := WorkspaceProcessInput.Schema.Properties["data"].(map[string]any)
	require.NotContains(t, data, "maxLength", "technical request-size guard is not a public product limit")
	require.Equal(t, mcpExecutionAnnotations, WorkspaceProcessInput.MCPAnnotations)
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
	require.Equal(t, "  partial output \n", running.Output)
	require.Nil(t, running.ExitCode, "running processes must not expose a fake exit code")
	require.Equal(t, "process-running", running.ProcessID)
	require.True(t, running.Running)
	require.Equal(t, truncation, running.Truncated)

	exitCode := 7
	completed := workspaceProcessResult("process-completed", workspacesdk.ProcessOutputResponse{
		Output:   "\n done \n",
		Running:  false,
		ExitCode: &exitCode,
	})
	require.Equal(t, "\n done \n", completed.Output)
	require.NotNil(t, completed.ExitCode)
	require.Equal(t, 7, *completed.ExitCode)
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

func TestWorkspaceProcessConstants(t *testing.T) {
	t.Parallel()

	require.Equal(t, 5*time.Second, processSnapshotTimeout)
}
