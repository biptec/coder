package coderd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/httpmw"
	codermcp "github.com/coder/coder/v2/coderd/mcp"
	"github.com/coder/coder/v2/codersdk/toolsdk"
)

const (
	mcpTraceWriteTimeout = 2 * time.Second
	mcpTraceQueueSize    = 8192
	mcpTraceIDHeader     = "X-Coder-MCP-Trace-Id"
)

type mcpAgentTraceContextKey struct{}
type mcpTraceHTTPConnectionContextKey struct{}

type mcpTraceHTTPConnection struct {
	id         uuid.UUID
	conn       net.Conn
	acceptedAt time.Time

	mu          sync.Mutex
	lastState   http.ConnState
	lastStateAt time.Time
	traced      bool
	lastTraceID uuid.UUID
}

type mcpAgentTraceContext struct {
	traceID     uuid.UUID
	workspaceID uuid.UUID
}

func withMCPAgentTraceWorkspace(ctx context.Context, workspaceID uuid.UUID) context.Context {
	if workspaceID == uuid.Nil {
		return ctx
	}
	traceID, _ := toolsdk.MCPTraceIDFromContext(ctx)
	return context.WithValue(ctx, mcpAgentTraceContextKey{}, mcpAgentTraceContext{
		traceID:     traceID,
		workspaceID: workspaceID,
	})
}

func mcpAgentTraceMetadataFromContext(ctx context.Context) (uuid.UUID, uuid.UUID) {
	metadata, _ := ctx.Value(mcpAgentTraceContextKey{}).(mcpAgentTraceContext)
	if metadata.traceID == uuid.Nil {
		metadata.traceID, _ = toolsdk.MCPTraceIDFromContext(ctx)
	}
	if metadata.traceID == uuid.Nil {
		metadata.traceID, _ = codermcp.TraceIDFromContext(ctx)
	}
	return metadata.traceID, metadata.workspaceID
}

type mcpTraceWrite struct {
	traceID uuid.UUID
	stage   string
	apply   func(context.Context) error
	done    chan struct{}
}

type mcpTraceRecorder struct {
	ctx       context.Context
	db        database.Store
	logger    slog.Logger
	replicaID uuid.UUID
	queue     chan mcpTraceWrite
	dropped   atomic.Uint64

	httpConnectionsMu sync.Mutex
	httpConnections   map[net.Conn]*mcpTraceHTTPConnection
}

func newMCPTraceRecorder(ctx context.Context, db database.Store, logger slog.Logger, replicaID uuid.UUID) *mcpTraceRecorder {
	if ctx == nil {
		ctx = context.Background()
	}
	r := &mcpTraceRecorder{
		ctx:             ctx,
		db:              db,
		logger:          logger.Named("mcp-trace"),
		replicaID:       replicaID,
		queue:           make(chan mcpTraceWrite, mcpTraceQueueSize),
		httpConnections: make(map[net.Conn]*mcpTraceHTTPConnection),
	}
	go r.run()
	return r
}

func mcpTraceHTTPConnStateName(state http.ConnState) string {
	switch state {
	case http.StateNew:
		return "new"
	case http.StateActive:
		return "active"
	case http.StateIdle:
		return "idle"
	case http.StateHijacked:
		return "hijacked"
	case http.StateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// MCPTraceConnContext attaches a stable physical HTTP connection identifier to
// requests accepted on conn. Connections are only persisted if they later carry
// an MCP request.
func (api *API) MCPTraceConnContext(ctx context.Context, conn net.Conn) context.Context {
	if api == nil || api.mcpTrace == nil || conn == nil {
		return ctx
	}
	return api.mcpTrace.httpConnContext(ctx, conn)
}

// MCPTraceConnState records net/http connection-state transitions for physical
// connections that have carried at least one MCP request.
func (api *API) MCPTraceConnState(conn net.Conn, state http.ConnState) {
	if api == nil || api.mcpTrace == nil || conn == nil {
		return
	}
	api.mcpTrace.httpConnState(conn, state)
}

func (r *mcpTraceRecorder) httpConnContext(ctx context.Context, conn net.Conn) context.Context {
	if r == nil || conn == nil {
		return ctx
	}
	now := time.Now().UTC()
	meta := &mcpTraceHTTPConnection{
		id:          uuid.New(),
		conn:        conn,
		acceptedAt:  now,
		lastState:   http.StateNew,
		lastStateAt: now,
	}
	return context.WithValue(ctx, mcpTraceHTTPConnectionContextKey{}, meta)
}

func (r *mcpTraceRecorder) httpConnState(conn net.Conn, state http.ConnState) {
	if r == nil || conn == nil {
		return
	}

	r.httpConnectionsMu.Lock()
	meta := r.httpConnections[conn]
	if state == http.StateClosed || state == http.StateHijacked {
		delete(r.httpConnections, conn)
	}
	r.httpConnectionsMu.Unlock()
	if meta == nil {
		return
	}

	now := time.Now().UTC()
	meta.mu.Lock()
	meta.lastState = state
	meta.lastStateAt = now
	traced := meta.traced
	traceID := meta.lastTraceID
	connectionID := meta.id
	meta.mu.Unlock()

	if traced {
		r.httpConnectionEventAt(traceID, connectionID, mcpTraceHTTPConnStateName(state), now)
	}
}

func (r *mcpTraceRecorder) httpConnectionForRequest(ctx context.Context, traceID uuid.UUID) (connectionID uuid.UUID, firstMCPRequest bool, acceptedAt, activeAt time.Time) {
	meta, _ := ctx.Value(mcpTraceHTTPConnectionContextKey{}).(*mcpTraceHTTPConnection)
	if meta == nil || traceID == uuid.Nil {
		return uuid.Nil, false, time.Time{}, time.Time{}
	}

	now := time.Now().UTC()
	meta.mu.Lock()
	firstMCPRequest = !meta.traced
	meta.traced = true
	meta.lastTraceID = traceID
	connectionID = meta.id
	conn := meta.conn
	acceptedAt = meta.acceptedAt
	if firstMCPRequest {
		meta.lastState = http.StateActive
		meta.lastStateAt = now
		activeAt = now
	}
	meta.mu.Unlock()

	if firstMCPRequest {
		r.httpConnectionsMu.Lock()
		r.httpConnections[conn] = meta
		r.httpConnectionsMu.Unlock()
	}

	return connectionID, firstMCPRequest, acceptedAt, activeAt
}

func (r *mcpTraceRecorder) httpConnectionEventAt(traceID, connectionID uuid.UUID, state string, occurredAt time.Time) {
	if r == nil || traceID == uuid.Nil || connectionID == uuid.Nil || state == "" {
		return
	}
	details, err := json.Marshal(map[string]string{"connection_id": connectionID.String()})
	if err != nil {
		return
	}
	replicaID := r.replicaID
	r.enqueue(traceID, "http_connection_"+state, func(writeCtx context.Context) error {
		return r.db.InsertMCPTraceAgentEvent(writeCtx, database.InsertMCPTraceAgentEventParams{
			ID:          uuid.New(),
			RequestID:   traceID,
			ReplicaID:   replicaID,
			WorkspaceID: uuid.NullUUID{},
			AgentID:     uuid.Nil,
			Event:       "http_connection_" + state,
			Details:     string(details),
			OccurredAt:  occurredAt,
		})
	})
}

func (r *mcpTraceRecorder) run() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case write := <-r.queue:
			if write.done != nil {
				close(write.done)
				continue
			}
			if write.apply == nil {
				continue
			}
			writeCtx, cancel := context.WithTimeout(dbauthz.AsSystemRestricted(r.ctx), mcpTraceWriteTimeout)
			err := write.apply(writeCtx)
			cancel()
			if err != nil {
				r.logger.Debug(context.Background(), "persist MCP trace event",
					slog.Error(err),
					slog.F("stage", write.stage),
					slog.F("trace_id", write.traceID),
				)
			}
		}
	}
}

func (r *mcpTraceRecorder) enqueue(traceID uuid.UUID, stage string, apply func(context.Context) error) bool {
	if r == nil || r.db == nil || traceID == uuid.Nil {
		return false
	}
	write := mcpTraceWrite{traceID: traceID, stage: stage, apply: apply}
	select {
	case <-r.ctx.Done():
		return false
	case r.queue <- write:
		return true
	default:
		dropped := r.dropped.Add(1)
		if dropped == 1 || dropped%100 == 0 {
			r.logger.Warn(context.Background(), "MCP trace queue full; dropping diagnostic events",
				slog.F("dropped", dropped),
			)
		}
		return false
	}
}

// flush is only used by tests to wait until all previously queued trace writes
// have been processed. Production requests never wait for diagnostic writes.
func (r *mcpTraceRecorder) flush(ctx context.Context) error {
	if r == nil {
		return nil
	}
	done := make(chan struct{})
	write := mcpTraceWrite{stage: "flush", done: done}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.ctx.Done():
		return r.ctx.Err()
	case r.queue <- write:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.ctx.Done():
		return r.ctx.Err()
	case <-done:
		return nil
	}
}

func (r *mcpTraceRecorder) AgentEvent(ctx context.Context, agentID uuid.UUID, event string, details any) {
	traceID, workspaceID := mcpAgentTraceMetadataFromContext(ctx)
	if traceID == uuid.Nil {
		return
	}
	r.AgentEventFor(traceID, workspaceID, agentID, event, details)
}

func (r *mcpTraceRecorder) AgentEventFor(traceID, workspaceID, agentID uuid.UUID, event string, details any) {
	if r == nil || traceID == uuid.Nil || agentID == uuid.Nil || event == "" {
		return
	}
	detailsJSON := "{}"
	if details != nil {
		encoded, err := json.Marshal(details)
		if err != nil {
			r.logger.Debug(context.Background(), "marshal MCP agent trace details", slog.Error(err), slog.F("event", event))
		} else {
			detailsJSON = string(encoded)
		}
	}
	now := time.Now().UTC()
	replicaID := r.replicaID
	workspace := uuid.NullUUID{UUID: workspaceID, Valid: workspaceID != uuid.Nil}
	r.enqueue(traceID, "agent_"+event, func(writeCtx context.Context) error {
		return r.db.InsertMCPTraceAgentEvent(writeCtx, database.InsertMCPTraceAgentEventParams{
			ID:          uuid.New(),
			RequestID:   traceID,
			WorkspaceID: workspace,
			AgentID:     agentID,
			Event:       event,
			Details:     detailsJSON,
			OccurredAt:  now,
			ReplicaID:   replicaID,
		})
	})
}

func (r *mcpTraceRecorder) isMCPRequest(req *http.Request) bool {
	path := req.URL.Path
	return path == codermcp.MCPEndpoint || strings.HasPrefix(path, codermcp.MCPEndpoint+"/")
}

func (r *mcpTraceRecorder) begin(req *http.Request) (uuid.UUID, bool) {
	if r == nil || r.db == nil || !r.isMCPRequest(req) {
		return uuid.Nil, false
	}

	traceID := uuid.New()
	requestID, hasRequestID := httpmw.RequestIDOptional(req)
	now := time.Now().UTC()
	method := req.Method
	protocol := req.Proto
	sessionID := req.Header.Get("Mcp-Session-Id")
	replicaID := r.replicaID
	connectionID, firstMCPRequest, acceptedAt, activeAt := r.httpConnectionForRequest(req.Context(), traceID)

	r.enqueue(traceID, "http_received", func(ctx context.Context) error {
		if err := r.db.InsertMCPTraceRequest(ctx, database.InsertMCPTraceRequestParams{
			ID:             traceID,
			ReplicaID:      replicaID,
			CoderRequestID: uuid.NullUUID{UUID: requestID, Valid: hasRequestID},
			UserID:         uuid.NullUUID{},
			SessionID:      sessionID,
			HttpMethod:     method,
			HttpProtocol:   protocol,
			ReceivedAt:     now,
		}); err != nil {
			return err
		}
		if method != http.MethodGet {
			return nil
		}
		return r.db.InsertMCPTraceConnection(ctx, database.InsertMCPTraceConnectionParams{
			ID:           uuid.New(),
			RequestID:    traceID,
			ReplicaID:    replicaID,
			UserID:       uuid.NullUUID{},
			SessionID:    sessionID,
			HttpProtocol: protocol,
			OpenedAt:     now,
		})
	})
	if connectionID != uuid.Nil {
		r.httpConnectionEventAt(traceID, connectionID, "request", now)
		if firstMCPRequest {
			r.httpConnectionEventAt(traceID, connectionID, "new", acceptedAt)
			r.httpConnectionEventAt(traceID, connectionID, "active", activeAt)
		}
	}
	return traceID, true
}

func (r *mcpTraceRecorder) CoderRequestID(ctx context.Context, requestID uuid.UUID) {
	traceID, ok := codermcp.TraceIDFromContext(ctx)
	if !ok || requestID == uuid.Nil {
		return
	}
	r.enqueue(traceID, "coder_request_id", func(writeCtx context.Context) error {
		return r.db.UpdateMCPTraceRequestCoderRequestID(writeCtx, database.UpdateMCPTraceRequestCoderRequestIDParams{
			CoderRequestID: uuid.NullUUID{UUID: requestID, Valid: true},
			ID:             traceID,
		})
	})
}

func (r *mcpTraceRecorder) Authenticated(ctx context.Context, userID uuid.UUID) {
	traceID, ok := codermcp.TraceIDFromContext(ctx)
	if !ok {
		return
	}
	now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	user := uuid.NullUUID{UUID: userID, Valid: userID != uuid.Nil}
	r.enqueue(traceID, "authenticated", func(writeCtx context.Context) error {
		if err := r.db.UpdateMCPTraceRequestAuthenticated(writeCtx, database.UpdateMCPTraceRequestAuthenticatedParams{
			UserID:          user,
			AuthenticatedAt: now,
			ID:              traceID,
		}); err != nil {
			return err
		}
		return r.db.UpdateMCPTraceConnectionSession(writeCtx, database.UpdateMCPTraceConnectionSessionParams{
			UserID:    user,
			SessionID: "",
			RequestID: traceID,
		})
	})
}

func (r *mcpTraceRecorder) TransportEntered(ctx context.Context, sessionID string) {
	traceID, ok := codermcp.TraceIDFromContext(ctx)
	if !ok {
		return
	}
	now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	r.enqueue(traceID, "transport_entered", func(writeCtx context.Context) error {
		if err := r.db.UpdateMCPTraceRequestTransportEntered(writeCtx, database.UpdateMCPTraceRequestTransportEnteredParams{
			SessionID:          sessionID,
			TransportEnteredAt: now,
			ID:                 traceID,
		}); err != nil {
			return err
		}
		return r.db.UpdateMCPTraceConnectionSession(writeCtx, database.UpdateMCPTraceConnectionSessionParams{
			UserID:    uuid.NullUUID{},
			SessionID: sessionID,
			RequestID: traceID,
		})
	})
}

func (r *mcpTraceRecorder) Parsed(ctx context.Context, method mcpgo.MCPMethod, jsonrpcID, sessionID string) {
	traceID, ok := codermcp.TraceIDFromContext(ctx)
	if !ok {
		return
	}
	now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	r.enqueue(traceID, "parsed", func(writeCtx context.Context) error {
		return r.db.UpdateMCPTraceRequestParsed(writeCtx, database.UpdateMCPTraceRequestParsedParams{
			McpMethod: string(method),
			JsonrpcID: jsonrpcID,
			SessionID: sessionID,
			ParsedAt:  now,
			ID:        traceID,
		})
	})
}

func (r *mcpTraceRecorder) Dispatched(ctx context.Context, method mcpgo.MCPMethod, jsonrpcID, tool string) {
	traceID, ok := codermcp.TraceIDFromContext(ctx)
	if !ok {
		return
	}
	now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	r.enqueue(traceID, "dispatched", func(writeCtx context.Context) error {
		return r.db.UpdateMCPTraceRequestDispatched(writeCtx, database.UpdateMCPTraceRequestDispatchedParams{
			McpMethod:    string(method),
			JsonrpcID:    jsonrpcID,
			Tool:         tool,
			DispatchedAt: now,
			ID:           traceID,
		})
	})
}

func (r *mcpTraceRecorder) HandlerStarted(ctx context.Context, tool string) {
	traceID, ok := codermcp.TraceIDFromContext(ctx)
	if !ok {
		return
	}
	now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	r.enqueue(traceID, "handler_started", func(writeCtx context.Context) error {
		return r.db.UpdateMCPTraceRequestHandlerStarted(writeCtx, database.UpdateMCPTraceRequestHandlerStartedParams{
			Tool:             tool,
			HandlerStartedAt: now,
			ID:               traceID,
		})
	})
}

func (r *mcpTraceRecorder) HandlerFinished(ctx context.Context, _ string) {
	traceID, ok := codermcp.TraceIDFromContext(ctx)
	if !ok {
		return
	}
	now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	r.enqueue(traceID, "handler_finished", func(writeCtx context.Context) error {
		return r.db.UpdateMCPTraceRequestHandlerFinished(writeCtx, database.UpdateMCPTraceRequestHandlerFinishedParams{
			HandlerFinishedAt: now,
			ID:                traceID,
		})
	})
}

func (r *mcpTraceRecorder) MCPFinished(ctx context.Context) {
	traceID, ok := codermcp.TraceIDFromContext(ctx)
	if !ok {
		return
	}
	now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	r.enqueue(traceID, "mcp_finished", func(writeCtx context.Context) error {
		return r.db.UpdateMCPTraceRequestMCPFinished(writeCtx, database.UpdateMCPTraceRequestMCPFinishedParams{
			McpFinishedAt: now,
			ID:            traceID,
		})
	})
}

func (r *mcpTraceRecorder) SessionRegistered(ctx context.Context, sessionID string) {
	traceID, ok := codermcp.TraceIDFromContext(ctx)
	if !ok {
		return
	}
	now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	r.enqueue(traceID, "session_registered", func(writeCtx context.Context) error {
		return r.db.UpdateMCPTraceRequestSessionRegistered(writeCtx, database.UpdateMCPTraceRequestSessionRegisteredParams{
			SessionID:    sessionID,
			RegisteredAt: now,
			ID:           traceID,
		})
	})
}

func (r *mcpTraceRecorder) SessionUnregistered(ctx context.Context, sessionID string) {
	traceID, ok := codermcp.TraceIDFromContext(ctx)
	if !ok {
		return
	}
	now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	r.enqueue(traceID, "session_unregistered", func(writeCtx context.Context) error {
		return r.db.UpdateMCPTraceRequestSessionUnregistered(writeCtx, database.UpdateMCPTraceRequestSessionUnregisteredParams{
			SessionID:      sessionID,
			UnregisteredAt: now,
			ID:             traceID,
		})
	})
}

func (r *mcpTraceRecorder) responseProgress(ctx context.Context, bytesWritten int64) {
	traceID, ok := codermcp.TraceIDFromContext(ctx)
	if !ok {
		return
	}
	now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	r.enqueue(traceID, "response_writing", func(writeCtx context.Context) error {
		return r.db.UpdateMCPTraceRequestResponseProgress(writeCtx, database.UpdateMCPTraceRequestResponseProgressParams{
			WriteAt:      now,
			BytesWritten: bytesWritten,
			ID:           traceID,
		})
	})
}

type mcpTraceResponseMetrics struct {
	startedAt  sql.NullTime
	lastWrite  sql.NullTime
	bytes      int64
	writeCount int64
}

func (r *mcpTraceRecorder) finish(ctx context.Context, status, errorKind, lastStage string, httpStatus int, canceledAt sql.NullTime, metrics mcpTraceResponseMetrics, isConnection bool) {
	traceID, ok := codermcp.TraceIDFromContext(ctx)
	if !ok {
		return
	}
	now := sql.NullTime{Time: time.Now().UTC(), Valid: true}
	r.enqueue(traceID, lastStage, func(writeCtx context.Context) error {
		if err := r.db.FinishMCPTraceRequest(writeCtx, database.FinishMCPTraceRequestParams{
			Status:              status,
			ErrorKind:           errorKind,
			HttpStatus:          sql.NullInt32{Int32: int32(httpStatus), Valid: httpStatus > 0},
			ResponseStartedAt:   metrics.startedAt,
			LastResponseWriteAt: metrics.lastWrite,
			ResponseBytes:       metrics.bytes,
			ResponseWriteCount:  metrics.writeCount,
			CanceledAt:          canceledAt,
			FinishedAt:          now,
			LastStage:           lastStage,
			ID:                  traceID,
		}); err != nil {
			return err
		}
		if !isConnection {
			return nil
		}
		reason := errorKind
		if reason == "" {
			reason = "handler_returned"
		}
		return r.db.FinishMCPTraceConnection(writeCtx, database.FinishMCPTraceConnectionParams{
			Status:      status,
			ClosedAt:    now,
			CloseReason: reason,
			RequestID:   traceID,
		})
	})
}

func (api *API) mcpTraceMiddleware(next http.Handler) http.Handler {
	if api.mcpTrace == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		traceID, ok := api.mcpTrace.begin(req)
		if !ok {
			next.ServeHTTP(w, req)
			return
		}

		ctx := codermcp.WithTraceID(req.Context(), traceID)
		req = req.WithContext(ctx)
		w.Header().Set(mcpTraceIDHeader, traceID.String())

		statusCode := 0
		var writeErr error
		var responseMetrics mcpTraceResponseMetrics
		var mu sync.Mutex
		wrapped := httpsnoop.Wrap(w, httpsnoop.Hooks{
			WriteHeader: func(nextWriteHeader httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
				return func(code int) {
					nextWriteHeader(code)
					if code >= 200 {
						now := time.Now().UTC()
						mu.Lock()
						if statusCode == 0 {
							statusCode = code
						}
						if !responseMetrics.startedAt.Valid {
							responseMetrics.startedAt = sql.NullTime{Time: now, Valid: true}
						}
						responseMetrics.lastWrite = sql.NullTime{Time: now, Valid: true}
						mu.Unlock()
						api.mcpTrace.responseProgress(ctx, 0)
					}
				}
			},
			Write: func(nextWrite httpsnoop.WriteFunc) httpsnoop.WriteFunc {
				return func(p []byte) (int, error) {
					n, err := nextWrite(p)
					now := time.Now().UTC()
					mu.Lock()
					if n > 0 {
						if statusCode == 0 {
							statusCode = http.StatusOK
						}
						if !responseMetrics.startedAt.Valid {
							responseMetrics.startedAt = sql.NullTime{Time: now, Valid: true}
						}
						responseMetrics.lastWrite = sql.NullTime{Time: now, Valid: true}
						responseMetrics.bytes += int64(n)
						responseMetrics.writeCount++
					}
					if err != nil {
						writeErr = err
					}
					mu.Unlock()
					if n > 0 {
						api.mcpTrace.responseProgress(ctx, int64(n))
					}
					return n, err
				}
			},
		})

		defer func() {
			mu.Lock()
			code := statusCode
			err := writeErr
			metrics := responseMetrics
			mu.Unlock()

			status := "completed"
			errorKind := ""
			lastStage := "response_finished"
			canceledAt := sql.NullTime{}
			switch {
			case errors.Is(req.Context().Err(), context.Canceled):
				status = "canceled"
				errorKind = "context_canceled"
				lastStage = "canceled"
				canceledAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
			case errors.Is(req.Context().Err(), context.DeadlineExceeded):
				status = "canceled"
				errorKind = "deadline_exceeded"
				lastStage = "canceled"
				canceledAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
			case err != nil:
				status = "failed"
				errorKind = "response_write_error"
			case code >= http.StatusInternalServerError:
				status = "failed"
				errorKind = "http_5xx"
			case code == 0:
				lastStage = "response_not_started"
			}
			api.mcpTrace.finish(ctx, status, errorKind, lastStage, code, canceledAt, metrics, req.Method == http.MethodGet)
		}()

		next.ServeHTTP(wrapped, req)
	})
}

// mcpTraceRequestIDMiddleware runs immediately after AttachRequestID so the
// trace created at ingress can be correlated with Coder's normal request ID.
func (api *API) mcpTraceRequestIDMiddleware(next http.Handler) http.Handler {
	if api.mcpTrace == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if requestID, ok := httpmw.RequestIDOptional(req); ok {
			api.mcpTrace.CoderRequestID(req.Context(), requestID)
		}
		next.ServeHTTP(w, req)
	})
}
