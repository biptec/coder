package agentsemantic

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/agent/agentexec"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

func newSemanticTestAPI(t *testing.T) *API {
	t.Helper()
	manager := NewManager(context.Background(), slogtest.Make(t, nil), agentexec.DefaultExecer, nil)
	api := NewAPI(manager)
	t.Cleanup(func() {
		require.NoError(t, api.Close())
	})
	return api
}

func semanticPOST(t *testing.T, handler http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestAPIFindSymbolsLiveGopls(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/gopls"); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/api\n\ngo 1.26\n"), 0o600))
	path := filepath.Join(root, "api.go")
	require.NoError(t, os.WriteFile(path, []byte(`package api

func SemanticTarget() {}
`), 0o600))

	api := newSemanticTestAPI(t)
	server := httptest.NewServer(api.Routes())
	t.Cleanup(server.Close)

	ctx := testutil.Context(t, testutil.WaitLong)
	data, err := json.Marshal(workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "SemanticTarget", Match: "exact", Limit: 20,
	})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/symbols", bytes.NewReader(data))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")

	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var result workspacesdk.SemanticFindSymbolsResponse
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	require.Len(t, result.Symbols, 1)
	require.Equal(t, "SemanticTarget", result.Symbols[0].Name)
	require.Equal(t, path, result.Symbols[0].Path)
}

func TestAPIStrictJSONRejectsUnknownField(t *testing.T) {
	t.Parallel()
	api := newSemanticTestAPI(t)
	response := semanticPOST(t, api.Routes(), "/symbols", map[string]any{
		"root": "/tmp", "query": "X", "limit": 20, "unexpected": true,
	})
	require.Equal(t, http.StatusBadRequest, response.Code)

	var semanticErr workspacesdk.SemanticErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &semanticErr))
	require.Equal(t, CodeInvalidPath, semanticErr.Code)
	require.Contains(t, semanticErr.Detail, "unknown field")
}

func TestAPIUnsupportedLanguageKeepsStableCode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "Example.java")
	require.NoError(t, os.WriteFile(path, []byte("class Example {}\n"), 0o600))

	api := newSemanticTestAPI(t)
	response := semanticPOST(t, api.Routes(), "/references", workspacesdk.SemanticFindReferencesRequest{
		Target: workspacesdk.SemanticTarget{Path: path, Line: 1, Column: 1},
		Limit:  20,
	})
	require.Equal(t, http.StatusBadRequest, response.Code)

	var semanticErr workspacesdk.SemanticErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &semanticErr))
	require.Equal(t, CodeUnsupportedLanguage, semanticErr.Code)
	require.Contains(t, semanticErr.Message, "No semantic backend is available")
}
