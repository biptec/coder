package agentproc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

func postInput(t *testing.T, handler http.Handler, id string, req workspacesdk.ProcessInputRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitLong)
	defer cancel()
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("/%s/input", id), bytes.NewReader(body))
	handler.ServeHTTP(w, r)
	return w
}

func getOutputCursor(t *testing.T, handler http.Handler, id string, cursor int64, limit int) workspacesdk.ProcessOutputResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitLong)
	defer cancel()
	w := httptest.NewRecorder()
	path := fmt.Sprintf("/%s/output?cursor=%d&limit=%d", id, cursor, limit)
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	handler.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var resp workspacesdk.ProcessOutputResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp
}

func getOutputCursorWait(t *testing.T, handler http.Handler, id string, cursor int64, limit int) workspacesdk.ProcessOutputResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitLong)
	defer cancel()
	w := httptest.NewRecorder()
	path := fmt.Sprintf("/%s/output?cursor=%d&limit=%d&wait=true", id, cursor, limit)
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	handler.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	var resp workspacesdk.ProcessOutputResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp
}

func TestPhase2DirectArgvExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable paths")
	}
	t.Parallel()

	handler := newTestAPI(t)
	id := startAndGetID(t, handler, workspacesdk.StartProcessRequest{
		Argv: []string{"/usr/bin/printf", "%s", "literal; echo shell-was-not-used"},
	})
	resp := waitForExit(t, handler, id)
	require.Equal(t, "literal; echo shell-was-not-used", resp.Output)
	require.NotNil(t, resp.ExitCode)
	require.Zero(t, *resp.ExitCode)

	w := postStart(t, handler, workspacesdk.StartProcessRequest{
		Command: "echo shell",
		Argv:    []string{"/usr/bin/printf", "argv"},
	})
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPhase2InputSizeLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable paths")
	}
	t.Parallel()

	handler := newTestAPI(t)
	tooLarge := strings.Repeat("x", workspacesdk.MaxProcessInputBytes+1)
	w := postStart(t, handler, workspacesdk.StartProcessRequest{
		Argv:  []string{"/bin/cat"},
		Stdin: tooLarge,
	})
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "stdin cannot exceed")

	id := startAndGetID(t, handler, workspacesdk.StartProcessRequest{
		Argv:        []string{"/bin/cat"},
		Interactive: true,
	})
	w = postInput(t, handler, id, workspacesdk.ProcessInputRequest{Data: tooLarge})
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "data cannot exceed")
	w = postInput(t, handler, id, workspacesdk.ProcessInputRequest{Close: true})
	require.Equal(t, http.StatusOK, w.Code)
	_ = waitForExit(t, handler, id)
}

func TestPhase2InteractiveInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable paths")
	}
	t.Parallel()

	handler := newTestAPI(t)
	id := startAndGetID(t, handler, workspacesdk.StartProcessRequest{
		Argv:        []string{"/bin/cat"},
		Interactive: true,
	})

	w := postInput(t, handler, id, workspacesdk.ProcessInputRequest{Data: "hello interactive\n", Close: true})
	require.Equal(t, http.StatusOK, w.Code)
	resp := waitForExit(t, handler, id)
	require.Equal(t, "hello interactive\n", resp.Output)
	require.NotNil(t, resp.ExitCode)
	require.Zero(t, *resp.ExitCode)
}

func TestPhase2IncrementalOutputCursor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell commands")
	}
	t.Parallel()

	handler := newTestAPI(t)
	id := startAndGetID(t, handler, workspacesdk.StartProcessRequest{
		Argv: []string{"/usr/bin/printf", "abcdef"},
	})
	_ = waitForExit(t, handler, id)

	first := getOutputCursor(t, handler, id, 0, 3)
	require.Equal(t, "abc", first.Output)
	require.Equal(t, int64(3), first.NextCursor)
	require.True(t, first.HasMore)
	require.Zero(t, first.GapBytes)

	second := getOutputCursor(t, handler, id, first.NextCursor, 3)
	require.Equal(t, "def", second.Output)
	require.Equal(t, int64(6), second.NextCursor)
	require.False(t, second.HasMore)
	require.Zero(t, second.GapBytes)

	largeID := startAndGetID(t, handler, workspacesdk.StartProcessRequest{
		Command: "yes x | head -c 70000",
	})
	_ = waitForExit(t, handler, largeID)
	gap := getOutputCursor(t, handler, largeID, 0, 16)
	require.Positive(t, gap.GapBytes)
	require.Greater(t, gap.NextCursor, gap.GapBytes)
	require.Len(t, gap.Output, 16)

	futureID := startAndGetID(t, handler, workspacesdk.StartProcessRequest{
		Command: "sleep 0.05; printf x; sleep 1",
	})
	future := getOutputCursorWait(t, handler, futureID, 999999, 16)
	require.Equal(t, "x", future.Output)
	require.Equal(t, int64(1), future.NextCursor)
	require.True(t, future.Running, "future cursor must clamp to the current stream end instead of waiting for an impossible offset")
}
