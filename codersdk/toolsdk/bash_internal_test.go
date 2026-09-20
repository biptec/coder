package toolsdk

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk/agentconnmock"
)

func TestObserveWorkspaceProcessReturnsLastSnapshotAtObservationBoundary(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	const processID = "process-id"

	first := conn.EXPECT().
		ProcessOutput(gomock.Any(), processID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, opts *workspacesdk.ProcessOutputOptions) (workspacesdk.ProcessOutputResponse, error) {
			require.NotNil(t, opts)
			require.True(t, opts.Wait)
			return workspacesdk.ProcessOutputResponse{Running: true, Output: "still working"}, nil
		})
	conn.EXPECT().
		ProcessOutput(gomock.Any(), processID, gomock.Any()).
		DoAndReturn(func(callCtx context.Context, _ string, opts *workspacesdk.ProcessOutputOptions) (workspacesdk.ProcessOutputResponse, error) {
			require.NotNil(t, opts)
			require.True(t, opts.Wait)
			<-callCtx.Done()
			return workspacesdk.ProcessOutputResponse{}, callCtx.Err()
		}).
		After(first)

	started := time.Now()
	resp, err := observeWorkspaceProcess(context.Background(), conn, processID, mcpObservationBudget{deadline: time.Now().Add(20 * time.Millisecond)})
	require.NoError(t, err)
	require.True(t, resp.Running)
	require.Equal(t, "still working", resp.Output)
	require.Nil(t, resp.ExitCode)
	require.Less(t, time.Since(started), time.Second)
}

func TestObserveWorkspaceProcessStopsOnCallerCancel(t *testing.T) {
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
		})

	_, err := observeWorkspaceProcess(ctx, conn, processID, mcpObservationBudget{deadline: time.Now().Add(time.Second)})
	require.ErrorIs(t, err, context.Canceled)
}

func TestStartWorkspaceProcessObservationDeadlineUsesPublicRecoveryTool(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	budget := mcpObservationBudget{deadline: time.Now().Add(20 * time.Millisecond), window: 20 * time.Millisecond}

	conn.EXPECT().StartProcess(gomock.Any(), gomock.Any()).DoAndReturn(
		func(callCtx context.Context, _ workspacesdk.StartProcessRequest) (workspacesdk.StartProcessResponse, error) {
			<-callCtx.Done()
			return workspacesdk.StartProcessResponse{}, callCtx.Err()
		},
	)

	ctx := WithInvocationTool(context.Background(), "start_process")
	_, err := startWorkspaceProcessWithinObservation(ctx, conn, workspacesdk.StartProcessRequest{Argv: []string{"sleep", "600"}}, budget)
	require.Error(t, err)
	require.ErrorContains(t, err, "list_sessions")
	require.NotContains(t, err.Error(), "process_list")
}

func TestStartWorkspaceProcessObservationDeadlineIsRecoverable(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	conn := agentconnmock.NewMockAgentConn(ctrl)
	budget := mcpObservationBudget{deadline: time.Now().Add(20 * time.Millisecond)}

	conn.EXPECT().
		StartProcess(gomock.Any(), gomock.Any()).
		DoAndReturn(func(callCtx context.Context, _ workspacesdk.StartProcessRequest) (workspacesdk.StartProcessResponse, error) {
			<-callCtx.Done()
			return workspacesdk.StartProcessResponse{}, callCtx.Err()
		})

	_, err := startWorkspaceProcessWithinObservation(context.Background(), conn, workspacesdk.StartProcessRequest{Command: "sleep 600"}, budget)
	require.Error(t, err)
	require.ErrorContains(t, err, "process may already have been submitted")
	require.ErrorContains(t, err, "process_list")
}
