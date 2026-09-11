package toolsdk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
)

func TestWaitForBashCompletionHasNoInternalDeadline(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	const processID = "process-id"

	calls := 0
	exitCode := 0
	conn.EXPECT().
		ProcessOutput(gomock.Any(), processID, gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ string, opts *workspacesdk.ProcessOutputOptions) (workspacesdk.ProcessOutputResponse, error) {
			require.NotNil(t, opts)
			require.True(t, opts.Wait)
			_, hasDeadline := ctx.Deadline()
			require.False(t, hasDeadline, "bash wait must not add an execution deadline")

			calls++
			if calls == 1 {
				return workspacesdk.ProcessOutputResponse{Running: true}, nil
			}
			return workspacesdk.ProcessOutputResponse{Running: false, ExitCode: &exitCode, Output: "done"}, nil
		}).
		Times(2)

	resp, err := waitForBashCompletion(context.Background(), conn, processID)
	require.NoError(t, err)
	require.False(t, resp.Running)
	require.NotNil(t, resp.ExitCode)
	require.Zero(t, *resp.ExitCode)
	require.Equal(t, "done", resp.Output)
	require.Equal(t, 2, calls)
}

func TestWaitForBashCompletionStopsOnCallerCancel(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	const processID = "process-id"

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn.EXPECT().
		ProcessOutput(gomock.Any(), processID, gomock.Any()).
		DoAndReturn(func(callCtx context.Context, _ string, opts *workspacesdk.ProcessOutputOptions) (workspacesdk.ProcessOutputResponse, error) {
			require.NotNil(t, opts)
			require.True(t, opts.Wait)
			return workspacesdk.ProcessOutputResponse{}, callCtx.Err()
		}).
		Times(1)

	_, err := waitForBashCompletion(ctx, conn, processID)
	require.ErrorIs(t, err, context.Canceled)
}
