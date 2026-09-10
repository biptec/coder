package mcp

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type traceIDContextKey struct{}

// WithTraceID attaches the database trace row ID to an MCP HTTP request.
func WithTraceID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, traceIDContextKey{}, id)
}

// TraceIDFromContext returns the MCP diagnostic trace row ID when tracing is enabled.
func TraceIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(traceIDContextKey{}).(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// TraceRecorder receives lifecycle markers from the MCP transport and dispatcher.
// Implementations must be best-effort: diagnostics must never change MCP behavior.
type TraceRecorder interface {
	TransportEntered(ctx context.Context, sessionID string)
	Parsed(ctx context.Context, method mcpgo.MCPMethod, jsonrpcID, sessionID string)
	Dispatched(ctx context.Context, method mcpgo.MCPMethod, jsonrpcID, tool string)
	HandlerStarted(ctx context.Context, tool string)
	HandlerFinished(ctx context.Context, tool string)
	MCPFinished(ctx context.Context)
	SessionRegistered(ctx context.Context, sessionID string)
	SessionUnregistered(ctx context.Context, sessionID string)
}

func (s *Server) withTraceTracking(tool server.ServerTool, toolName string) server.ServerTool {
	if s.traceRecorder == nil {
		return tool
	}
	original := tool.Handler
	tool.Handler = func(ctx context.Context, request mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		s.traceRecorder.HandlerStarted(ctx, toolName)
		defer s.traceRecorder.HandlerFinished(ctx, toolName)
		return original(ctx, request)
	}
	return tool
}

func traceSessionID(ctx context.Context) string {
	session := server.ClientSessionFromContext(ctx)
	if session == nil {
		return ""
	}
	return session.SessionID()
}

func traceJSONRPCID(id any) string {
	if id == nil {
		return ""
	}
	return fmt.Sprint(id)
}
