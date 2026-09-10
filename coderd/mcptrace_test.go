package coderd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/coder/v2/coderd/httpmw"
	codermcp "github.com/coder/coder/v2/coderd/mcp"
	"github.com/coder/coder/v2/testutil"
)

func TestMCPTraceMiddlewareRequestLifecycle(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctrl := gomock.NewController(t)
	db := dbmock.NewMockStore(ctrl)
	recorder := newMCPTraceRecorder(ctx, db, testutil.Logger(t), uuid.New())
	api := &API{mcpTrace: recorder}
	var dbTraceID uuid.UUID
	var contextTraceID uuid.UUID
	var coderRequestID uuid.UUID

	gomock.InOrder(
		db.EXPECT().InsertMCPTraceRequest(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params database.InsertMCPTraceRequestParams) error {
				dbTraceID = params.ID
				require.NotEqual(t, uuid.Nil, dbTraceID)
				require.False(t, params.CoderRequestID.Valid)
				require.Equal(t, http.MethodPost, params.HttpMethod)
				return nil
			},
		),
		db.EXPECT().UpdateMCPTraceRequestCoderRequestID(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params database.UpdateMCPTraceRequestCoderRequestIDParams) error {
				require.Equal(t, dbTraceID, params.ID)
				require.True(t, params.CoderRequestID.Valid)
				coderRequestID = params.CoderRequestID.UUID
				require.NotEqual(t, uuid.Nil, coderRequestID)
				return nil
			},
		),
		db.EXPECT().UpdateMCPTraceRequestResponseProgress(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params database.UpdateMCPTraceRequestResponseProgressParams) error {
				require.Equal(t, dbTraceID, params.ID)
				require.Equal(t, int64(2), params.BytesWritten)
				return nil
			},
		),
		db.EXPECT().FinishMCPTraceRequest(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params database.FinishMCPTraceRequestParams) error {
				require.Equal(t, dbTraceID, params.ID)
				require.Equal(t, "completed", params.Status)
				require.Equal(t, "response_finished", params.LastStage)
				require.Equal(t, int32(http.StatusOK), params.HttpStatus.Int32)
				require.Equal(t, int64(2), params.ResponseBytes)
				require.Equal(t, int64(1), params.ResponseWriteCount)
				return nil
			},
		),
	)

	inner := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotTraceID, ok := codermcp.TraceIDFromContext(req.Context())
		require.True(t, ok)
		contextTraceID = gotTraceID
		require.NotEqual(t, uuid.Nil, contextTraceID)
		gotRequestID, ok := httpmw.RequestIDOptional(req)
		require.True(t, ok)
		require.NotEqual(t, uuid.Nil, gotRequestID)
		_, err := w.Write([]byte("ok"))
		require.NoError(t, err)
	})
	handler := api.mcpTraceMiddleware(httpmw.AttachRequestID(api.mcpTraceRequestIDMiddleware(inner)))

	req := httptest.NewRequest(http.MethodPost, codermcp.MCPEndpoint, nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)
	require.NoError(t, recorder.flush(testContext(t)))

	require.NotEqual(t, uuid.Nil, coderRequestID)
	require.Equal(t, dbTraceID, contextTraceID)
	require.Equal(t, coderRequestID.String(), rw.Header().Get("X-Coder-Request-Id"))
	require.Equal(t, dbTraceID.String(), rw.Header().Get(mcpTraceIDHeader))
}

func TestMCPTraceMiddlewareConnectionLifecycle(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctrl := gomock.NewController(t)
	db := dbmock.NewMockStore(ctrl)
	recorder := newMCPTraceRecorder(ctx, db, testutil.Logger(t), uuid.New())
	api := &API{mcpTrace: recorder}
	sessionID := "session-test"
	var traceID uuid.UUID

	gomock.InOrder(
		db.EXPECT().InsertMCPTraceRequest(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params database.InsertMCPTraceRequestParams) error {
				traceID = params.ID
				require.Equal(t, sessionID, params.SessionID)
				require.Equal(t, http.MethodGet, params.HttpMethod)
				return nil
			},
		),
		db.EXPECT().InsertMCPTraceConnection(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params database.InsertMCPTraceConnectionParams) error {
				require.Equal(t, traceID, params.RequestID)
				require.Equal(t, sessionID, params.SessionID)
				return nil
			},
		),
		db.EXPECT().UpdateMCPTraceRequestResponseProgress(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params database.UpdateMCPTraceRequestResponseProgressParams) error {
				require.Equal(t, traceID, params.ID)
				require.Zero(t, params.BytesWritten)
				return nil
			},
		),
		db.EXPECT().FinishMCPTraceRequest(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params database.FinishMCPTraceRequestParams) error {
				require.Equal(t, traceID, params.ID)
				require.Equal(t, "completed", params.Status)
				require.Equal(t, int32(http.StatusOK), params.HttpStatus.Int32)
				return nil
			},
		),
		db.EXPECT().FinishMCPTraceConnection(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, params database.FinishMCPTraceConnectionParams) error {
				require.Equal(t, traceID, params.RequestID)
				require.Equal(t, "completed", params.Status)
				require.Equal(t, "handler_returned", params.CloseReason)
				return nil
			},
		),
	)

	handler := api.mcpTraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, ok := codermcp.TraceIDFromContext(req.Context())
		require.True(t, ok)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, codermcp.MCPEndpoint, nil)
	req.Header.Set("Mcp-Session-Id", sessionID)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)
	require.NoError(t, recorder.flush(testContext(t)))
	require.Equal(t, traceID.String(), rw.Header().Get(mcpTraceIDHeader))
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}
