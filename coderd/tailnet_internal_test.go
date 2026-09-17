package coderd

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/toolsdk"
	"github.com/coder/coder/v2/testutil"
)

type testCloserWaiter struct {
	wait chan error
}

func (t *testCloserWaiter) Close(context.Context) error { return nil }
func (t *testCloserWaiter) Wait() <-chan error          { return t.wait }

func TestTracingCloserWaiterPreservesWaitResult(t *testing.T) {
	t.Parallel()

	inner := &testCloserWaiter{wait: make(chan error, 1)}
	wantErr := context.DeadlineExceeded
	observed := make(chan error, 1)
	wrapped := newTracingCloserWaiter(inner, func(err error) { observed <- err })

	inner.wait <- wantErr

	require.ErrorIs(t, <-observed, wantErr)
	require.ErrorIs(t, <-wrapped.Wait(), wantErr)
}

func TestMultiAgentControllerIdleTimeoutConfiguration(t *testing.T) {
	t.Parallel()

	logger := testutil.Logger(t)
	tracer := trace.NewNoopTracerProvider().Tracer("test")

	configured := NewMultiAgentController(context.Background(), logger, tracer, nil, 120*time.Minute)
	t.Cleanup(configured.Close)
	require.Equal(t, 120*time.Minute, configured.idleTimeout)

	defaulted := NewMultiAgentController(context.Background(), logger, tracer, nil, 0)
	t.Cleanup(defaulted.Close)
	require.Equal(t, codersdk.DefaultServerTailnetAgentIdleTimeout, defaulted.idleTimeout)
}

func TestMultiAgentControllerMCPTraceLifecycle(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ctrl := gomock.NewController(t)
	db := dbmock.NewMockStore(ctrl)
	recorder := newMCPTraceRecorder(ctx, db, testutil.Logger(t), uuid.New())

	traceID := uuid.New()
	workspaceID := uuid.New()
	agentID := uuid.New()
	requestCtx := toolsdk.WithMCPTraceID(context.Background(), traceID)
	requestCtx = withMCPAgentTraceWorkspace(requestCtx, workspaceID)

	var events []database.InsertMCPTraceAgentEventParams
	db.EXPECT().InsertMCPTraceAgentEvent(gomock.Any(), gomock.Any()).Times(7).DoAndReturn(
		func(_ context.Context, params database.InsertMCPTraceAgentEventParams) error {
			events = append(events, params)
			return nil
		},
	)

	controller := NewMultiAgentController(
		context.Background(),
		testutil.Logger(t),
		trace.NewNoopTracerProvider().Tracer("test"),
		nil,
		30*time.Minute,
	)
	t.Cleanup(controller.Close)
	controller.setMCPTraceRecorder(recorder)

	require.NoError(t, controller.ensureAgent(requestCtx, agentID))
	release := controller.acquireTicket(requestCtx, agentID)
	release()
	controller.traceCoordinationDisconnected(io.EOF)

	controller.mu.Lock()
	state := controller.connectionTimes[agentID]
	state.lastConnection = time.Now().Add(-31 * time.Minute)
	controller.connectionTimes[agentID] = state
	controller.mu.Unlock()
	controller.doExpireOldAgents(context.Background(), 30*time.Minute)

	require.NoError(t, recorder.flush(testContext(t)))
	require.Len(t, events, 7)
	require.Equal(t, []string{
		"ensure_agent",
		"add_tunnel_deferred",
		"ticket_acquired",
		"ticket_released",
		"coordination_disconnected",
		"idle_timeout_expired",
		"remove_tunnel_deferred",
	}, []string{events[0].Event, events[1].Event, events[2].Event, events[3].Event, events[4].Event, events[5].Event, events[6].Event})
	for _, event := range events {
		require.Equal(t, traceID, event.RequestID)
		require.True(t, event.WorkspaceID.Valid)
		require.Equal(t, workspaceID, event.WorkspaceID.UUID)
		require.Equal(t, agentID, event.AgentID)
		require.NotEmpty(t, event.Details)
	}

	controller.mu.Lock()
	_, stillTracked := controller.connectionTimes[agentID]
	controller.mu.Unlock()
	require.False(t, stillTracked)
}
