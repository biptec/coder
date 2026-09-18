package database_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
)

func TestMCPTraceLifecycleAndRetention(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, _ := dbtestutil.NewDB(t)

	traceID := uuid.New()
	replicaID := uuid.New()
	requestID := uuid.New()
	startedAt := time.Now().UTC().Add(-25 * time.Hour).Round(time.Microsecond)

	require.NoError(t, db.InsertMCPTraceRequest(ctx, database.InsertMCPTraceRequestParams{
		ID:             traceID,
		ReplicaID:      replicaID,
		CoderRequestID: uuid.NullUUID{},
		SessionID:      "session-test",
		HttpMethod:     "GET",
		HttpProtocol:   "HTTP/2.0",
		ReceivedAt:     startedAt,
	}))
	require.NoError(t, db.InsertMCPTraceConnection(ctx, database.InsertMCPTraceConnectionParams{
		ID:           uuid.New(),
		RequestID:    traceID,
		ReplicaID:    replicaID,
		SessionID:    "session-test",
		HttpProtocol: "HTTP/2.0",
		OpenedAt:     startedAt,
	}))
	require.NoError(t, db.InsertMCPTraceAgentEvent(ctx, database.InsertMCPTraceAgentEventParams{
		ID:         uuid.New(),
		RequestID:  traceID,
		ReplicaID:  replicaID,
		AgentID:    uuid.New(),
		Event:      "ensure_agent",
		Details:    `{"already_subscribed":true}`,
		OccurredAt: startedAt.Add(time.Second),
	}))

	now := time.Now().UTC().Round(time.Microsecond)
	require.NoError(t, db.UpdateMCPTraceRequestCoderRequestID(ctx, database.UpdateMCPTraceRequestCoderRequestIDParams{
		CoderRequestID: uuid.NullUUID{UUID: requestID, Valid: true},
		ID:             traceID,
	}))
	require.NoError(t, db.UpdateMCPTraceRequestTransportEntered(ctx, database.UpdateMCPTraceRequestTransportEnteredParams{
		SessionID:          "session-test",
		TransportEnteredAt: sql.NullTime{Time: now, Valid: true},
		ID:                 traceID,
	}))
	require.NoError(t, db.UpdateMCPTraceRequestParsed(ctx, database.UpdateMCPTraceRequestParsedParams{
		McpMethod: "tools/call",
		JsonrpcID: "42",
		SessionID: "session-test",
		ParsedAt:  sql.NullTime{Time: now, Valid: true},
		ID:        traceID,
	}))
	require.NoError(t, db.UpdateMCPTraceRequestSessionRegistered(ctx, database.UpdateMCPTraceRequestSessionRegisteredParams{
		SessionID:    "session-test",
		RegisteredAt: sql.NullTime{Time: now, Valid: true},
		ID:           traceID,
	}))
	require.NoError(t, db.UpdateMCPTraceRequestDispatched(ctx, database.UpdateMCPTraceRequestDispatchedParams{
		McpMethod:    "tools/call",
		JsonrpcID:    "42",
		Tool:         "exec",
		DispatchedAt: sql.NullTime{Time: now, Valid: true},
		ID:           traceID,
	}))
	require.NoError(t, db.UpdateMCPTraceRequestHandlerStarted(ctx, database.UpdateMCPTraceRequestHandlerStartedParams{
		Tool:             "exec",
		HandlerStartedAt: sql.NullTime{Time: now, Valid: true},
		ID:               traceID,
	}))
	require.NoError(t, db.UpdateMCPTraceRequestHandlerFinished(ctx, database.UpdateMCPTraceRequestHandlerFinishedParams{
		HandlerFinishedAt: sql.NullTime{Time: now, Valid: true},
		ID:                traceID,
	}))
	require.NoError(t, db.UpdateMCPTraceRequestMCPFinished(ctx, database.UpdateMCPTraceRequestMCPFinishedParams{
		McpFinishedAt: sql.NullTime{Time: now, Valid: true},
		ID:            traceID,
	}))
	require.NoError(t, db.UpdateMCPTraceRequestResponseProgress(ctx, database.UpdateMCPTraceRequestResponseProgressParams{
		WriteAt:      sql.NullTime{Time: now, Valid: true},
		BytesWritten: 17,
		ID:           traceID,
	}))
	require.NoError(t, db.UpdateMCPTraceRequestSessionUnregistered(ctx, database.UpdateMCPTraceRequestSessionUnregisteredParams{
		SessionID:      "session-test",
		UnregisteredAt: sql.NullTime{Time: now, Valid: true},
		ID:             traceID,
	}))
	require.NoError(t, db.FinishMCPTraceRequest(ctx, database.FinishMCPTraceRequestParams{
		Status:     "completed",
		HttpStatus: sql.NullInt32{Int32: 200, Valid: true},
		FinishedAt: sql.NullTime{Time: now, Valid: true},
		LastStage:  "response_finished",
		ID:         traceID,
	}))
	require.NoError(t, db.FinishMCPTraceConnection(ctx, database.FinishMCPTraceConnectionParams{
		Status:      "completed",
		ClosedAt:    sql.NullTime{Time: now, Valid: true},
		CloseReason: "handler_returned",
		RequestID:   traceID,
	}))

	request, err := db.GetMCPTraceRequestByID(ctx, traceID)
	require.NoError(t, err)
	require.True(t, request.CoderRequestID.Valid)
	require.Equal(t, requestID, request.CoderRequestID.UUID)
	require.Equal(t, "tools/call", request.McpMethod)
	require.Equal(t, "42", request.JsonrpcID)
	require.Equal(t, "exec", request.Tool)
	require.Equal(t, "completed", request.Status)
	require.Equal(t, "response_finished", request.LastStage)
	require.Equal(t, int64(17), request.ResponseBytes)
	require.Equal(t, int64(1), request.ResponseWriteCount)
	require.True(t, request.TransportEnteredAt.Valid)
	require.True(t, request.ParsedAt.Valid)
	require.True(t, request.DispatchedAt.Valid)
	require.True(t, request.HandlerStartedAt.Valid)
	require.True(t, request.HandlerFinishedAt.Valid)
	require.True(t, request.McpFinishedAt.Valid)
	require.True(t, request.SessionRegisteredAt.Valid)
	require.True(t, request.SessionUnregisteredAt.Valid)
	require.True(t, request.ResponseStartedAt.Valid)
	require.True(t, request.FinishedAt.Valid)

	connection, err := db.GetMCPTraceConnectionByRequestID(ctx, traceID)
	require.NoError(t, err)
	require.Equal(t, "session-test", connection.SessionID)
	require.Equal(t, "completed", connection.Status)
	require.True(t, connection.ClosedAt.Valid)

	agentEvents, err := db.GetMCPTraceAgentEventsByRequestID(ctx, traceID)
	require.NoError(t, err)
	require.Len(t, agentEvents, 1)
	require.Equal(t, "ensure_agent", agentEvents[0].Event)

	deleted, err := db.DeleteOldMCPTraceRequests(ctx, database.DeleteOldMCPTraceRequestsParams{
		BeforeTime: time.Now().UTC().Add(-24 * time.Hour),
		LimitCount: 100,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)

	_, err = db.GetMCPTraceRequestByID(ctx, traceID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	_, err = db.GetMCPTraceConnectionByRequestID(ctx, traceID)
	require.ErrorIs(t, err, sql.ErrNoRows)
	agentEvents, err = db.GetMCPTraceAgentEventsByRequestID(ctx, traceID)
	require.NoError(t, err)
	require.Empty(t, agentEvents, "agent trace events must be cascade-deleted with their 24h-retained parent trace")
}
