package agentfiles

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func newMutationObserverTestAPI(t *testing.T) (*API, afero.Fs, *[][]string) {
	t.Helper()

	filesystem := afero.NewMemMapFs()
	logger := slogtest.Make(t, &slogtest.Options{IgnoreErrors: true})
	events := make([][]string, 0)
	api := NewAPI(
		logger,
		filesystem,
		nil,
		WithMutationObserver(func(_ context.Context, paths []string) {
			events = append(events, slices.Clone(paths))
		}),
	)
	return api, filesystem, &events
}

func TestMutationObserverWriteFile(t *testing.T) {
	t.Parallel()

	api, filesystem, events := newMutationObserverTestAPI(t)
	require.NoError(t, filesystem.MkdirAll("/root", 0o755))

	req := httptest.NewRequest(http.MethodPost, "/write-file-strict?path=%2Froot%2Fnew.go", strings.NewReader("package root\n"))
	res := httptest.NewRecorder()
	api.Routes().ServeHTTP(res, req)

	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Equal(t, [][]string{{"/root/new.go"}}, *events)
}

func TestMutationObserverEditFiles(t *testing.T) {
	t.Parallel()

	api, filesystem, events := newMutationObserverTestAPI(t)
	require.NoError(t, filesystem.MkdirAll("/root", 0o755))
	require.NoError(t, afero.WriteFile(filesystem, "/root/edit.go", []byte("package root\n\nconst Value = 1\n"), 0o600))

	body, err := json.Marshal(workspacesdk.FileEditRequest{
		Files: []workspacesdk.FileEdits{{
			Path: "/root/edit.go",
			Edits: []workspacesdk.FileEdit{{
				Search:  "const Value = 1",
				Replace: "const Value = 2",
			}},
		}},
		ExactOnly: true,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/edit-files", bytes.NewReader(body))
	res := httptest.NewRecorder()
	api.Routes().ServeHTTP(res, req)

	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Equal(t, [][]string{{"/root/edit.go"}}, *events)
}

func TestMutationObserverEditFilesDryRunDoesNotNotify(t *testing.T) {
	t.Parallel()

	api, filesystem, events := newMutationObserverTestAPI(t)
	require.NoError(t, filesystem.MkdirAll("/root", 0o755))
	require.NoError(t, afero.WriteFile(filesystem, "/root/edit.go", []byte("package root\n\nconst Value = 1\n"), 0o600))

	body, err := json.Marshal(workspacesdk.FileEditRequest{
		Files: []workspacesdk.FileEdits{{
			Path: "/root/edit.go",
			Edits: []workspacesdk.FileEdit{{
				Search:  "const Value = 1",
				Replace: "const Value = 2",
			}},
		}},
		ExactOnly: true,
		DryRun:    true,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/edit-files", bytes.NewReader(body))
	res := httptest.NewRecorder()
	api.Routes().ServeHTTP(res, req)

	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Empty(t, *events)
}

func TestMutationObserverMoveFile(t *testing.T) {
	t.Parallel()

	api, filesystem, events := newMutationObserverTestAPI(t)
	require.NoError(t, filesystem.MkdirAll("/root", 0o755))
	require.NoError(t, afero.WriteFile(filesystem, "/root/source.go", []byte("package root\n"), 0o600))

	body, err := json.Marshal(workspacesdk.MoveFileRequest{
		Source: "/root/source.go",
		Dest:   "/root/dest.go",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/move-file", bytes.NewReader(body))
	res := httptest.NewRecorder()
	api.Routes().ServeHTTP(res, req)

	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Equal(t, [][]string{{"/root/source.go", "/root/dest.go"}}, *events)
}
