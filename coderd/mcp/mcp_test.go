package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpserver "github.com/coder/coder/v2/coderd/mcp"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/toolsdk"
	"github.com/coder/coder/v2/testutil"
)

func TestMCPServer_Creation(t *testing.T) {
	t.Parallel()

	logger := testutil.Logger(t)

	server, err := mcpserver.NewServer(logger)
	require.NoError(t, err)
	require.NotNil(t, server)
}

func TestMCPServer_Handler(t *testing.T) {
	t.Parallel()

	logger := testutil.Logger(t)

	server, err := mcpserver.NewServer(logger)
	require.NoError(t, err)

	// Test that server implements http.Handler interface
	var handler http.Handler = server
	require.NotNil(t, handler)
}

func TestMCPHTTP_InitializeRequest(t *testing.T) {
	t.Parallel()

	logger := testutil.Logger(t)

	server, err := mcpserver.NewServer(logger)
	require.NoError(t, err)

	// Use server directly as http.Handler
	handler := server

	// Create initialize request
	initRequest := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.LATEST_PROTOCOL_VERSION,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "test-client",
				"version": "1.0.0",
			},
		},
	}

	body, err := json.Marshal(initRequest)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json,text/event-stream")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Logf("Response body: %s", recorder.Body.String())
	}
	assert.Equal(t, http.StatusOK, recorder.Code)

	// Check that a session ID was returned
	sessionID := recorder.Header().Get("Mcp-Session-Id")
	assert.NotEmpty(t, sessionID)

	// Parse response
	var response map[string]any
	err = json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "2.0", response["jsonrpc"])
	assert.Equal(t, float64(1), response["id"])

	result, ok := response["result"].(map[string]any)
	require.True(t, ok)

	assert.Equal(t, mcp.LATEST_PROTOCOL_VERSION, result["protocolVersion"])
	assert.Contains(t, result, "capabilities")
	assert.Contains(t, result, "serverInfo")
}

type traceEventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *traceEventRecorder) add(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *traceEventRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *traceEventRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

func (r *traceEventRecorder) TransportEntered(context.Context, string) { r.add("transport") }
func (r *traceEventRecorder) Parsed(_ context.Context, method mcp.MCPMethod, _, _ string) {
	r.add("parsed:" + string(method))
}

func (r *traceEventRecorder) Dispatched(_ context.Context, method mcp.MCPMethod, _, tool string) {
	r.add("dispatched:" + string(method) + ":" + tool)
}

func (r *traceEventRecorder) HandlerStarted(_ context.Context, tool string) {
	r.add("handler_started:" + tool)
}

func (r *traceEventRecorder) HandlerFinished(_ context.Context, tool string) {
	r.add("handler_finished:" + tool)
}
func (r *traceEventRecorder) MCPFinished(context.Context) { r.add("mcp_finished") }
func (r *traceEventRecorder) SessionRegistered(_ context.Context, sessionID string) {
	r.add("session_registered:" + sessionID)
}

func (r *traceEventRecorder) SessionUnregistered(_ context.Context, sessionID string) {
	r.add("session_unregistered:" + sessionID)
}

func TestMCPHTTP_TraceLifecycle(t *testing.T) {
	t.Parallel()

	logger := testutil.Logger(t)
	server, err := mcpserver.NewServer(logger)
	require.NoError(t, err)

	trace := &traceEventRecorder{}
	server.SetTraceRecorder(trace)
	server.SetActivityStore(mcpserver.NewActivityStore(100), "trace-test-user")
	client := codersdk.New(testutil.MustURL(t, "http://not-used"))
	require.NoError(t, server.RegisterTools(client))

	initBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.LATEST_PROTOCOL_VERSION,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "trace-test",
				"version": "1.0.0",
			},
		},
	})
	require.NoError(t, err)
	initReq := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(initBody))
	initReq = initReq.WithContext(mcpserver.WithTraceID(initReq.Context(), uuid.New()))
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Accept", "application/json,text/event-stream")
	initRecorder := httptest.NewRecorder()
	server.ServeHTTP(initRecorder, initReq)
	require.Equal(t, http.StatusOK, initRecorder.Code)
	sessionID := initRecorder.Header().Get("Mcp-Session-Id")
	require.NotEmpty(t, sessionID)

	initEvents := trace.snapshot()
	require.Contains(t, initEvents, "transport")
	require.Contains(t, initEvents, "parsed:initialize")
	require.Contains(t, initEvents, "dispatched:initialize:")
	require.Contains(t, initEvents, "session_registered:"+sessionID)
	require.Equal(t, "mcp_finished", initEvents[len(initEvents)-1])

	trace.reset()
	callBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "list_recent_tool_calls",
			"arguments": map[string]any{"workspace": "owner/workspace", "limit": 10},
		},
	})
	require.NoError(t, err)
	callReq := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(callBody))
	callReq = callReq.WithContext(mcpserver.WithTraceID(callReq.Context(), uuid.New()))
	callReq.Header.Set("Content-Type", "application/json")
	callReq.Header.Set("Accept", "application/json,text/event-stream")
	callReq.Header.Set("Mcp-Session-Id", sessionID)
	callRecorder := httptest.NewRecorder()
	server.ServeHTTP(callRecorder, callReq)
	require.Equal(t, http.StatusOK, callRecorder.Code)

	callEvents := trace.snapshot()
	require.Equal(t, []string{
		"transport",
		"parsed:tools/call",
		"dispatched:tools/call:list_recent_tool_calls",
		"handler_started:list_recent_tool_calls",
		"handler_finished:list_recent_tool_calls",
		"mcp_finished",
	}, callEvents)
}

func TestMCPHTTP_ToolRegistration(t *testing.T) {
	t.Parallel()

	logger := testutil.Logger(t)

	server, err := mcpserver.NewServer(logger)
	require.NoError(t, err)

	// Test registering tools with nil client should return error
	err = server.RegisterTools(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "client cannot be nil", "Should reject nil client with appropriate error message")

	// Test registering tools with valid client should succeed
	client := codersdk.New(testutil.MustURL(t, "http://not-used"))
	err = server.RegisterTools(client)
	require.NoError(t, err)

	// Verify that all expected tools are available in the toolsdk
	expectedToolCount := len(toolsdk.All)
	require.Greater(t, expectedToolCount, 0, "Should have some tools available")

	// Verify specific tools are present by checking tool names
	toolNames := make([]string, len(toolsdk.All))
	for i, tool := range toolsdk.All {
		toolNames[i] = tool.Name
	}
	require.Contains(t, toolNames, toolsdk.ToolNameReportTask, "Should include ReportTask (UserClientOptional)")
	require.Contains(t, toolNames, toolsdk.ToolNameGetAuthenticatedUser, "Should include GetAuthenticatedUser (requires auth)")
}
