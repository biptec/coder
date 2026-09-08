package toolsdk

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
)

func TestWaitForExecCompletionRepeatsWhileRunning(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	const processID = "process-id"

	calls := 0
	exitCode := 7
	conn.EXPECT().
		ProcessOutput(gomock.Any(), processID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, opts *workspacesdk.ProcessOutputOptions) (workspacesdk.ProcessOutputResponse, error) {
			require.NotNil(t, opts)
			require.True(t, opts.Wait)
			calls++
			if calls == 1 {
				return workspacesdk.ProcessOutputResponse{Running: true}, nil
			}
			return workspacesdk.ProcessOutputResponse{Running: false, ExitCode: &exitCode, Output: "done"}, nil
		}).
		Times(2)

	resp, err := waitForExecCompletion(context.Background(), conn, processID)
	require.NoError(t, err)
	require.False(t, resp.Running)
	require.NotNil(t, resp.ExitCode)
	require.Equal(t, 7, *resp.ExitCode)
	require.Equal(t, "done", resp.Output)
	require.Equal(t, 2, calls)
}
