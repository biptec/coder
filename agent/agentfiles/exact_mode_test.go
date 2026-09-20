package agentfiles_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3"
	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/agent/agentfiles"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func callEditFiles(t *testing.T, api *agentfiles.API, req workspacesdk.FileEditRequest) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(req)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	httpReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/edit-files", bytes.NewReader(body))
	api.Routes().ServeHTTP(rec, httpReq)
	return rec
}

//nolint:tparallel // subtests intentionally mutate the same in-memory file and must run serially.
func TestEditFilesExactOnly(t *testing.T) {
	t.Parallel()

	logger := slogtest.Make(t, nil).Leveled(slog.LevelDebug)
	fs := afero.NewMemMapFs()
	api := agentfiles.NewAPI(logger, fs, nil)

	const path = "/tmp/exact-only.txt"
	original := "\tvalue := 1\n"
	require.NoError(t, afero.WriteFile(fs, path, []byte(original), 0o644))

	//nolint:paralleltest // shares one mutable in-memory file with sibling subtests.
	t.Run("LegacyFuzzyCompatibility", func(t *testing.T) {
		require.NoError(t, afero.WriteFile(fs, path, []byte(original), 0o644))
		rec := callEditFiles(t, api, workspacesdk.FileEditRequest{
			Files: []workspacesdk.FileEdits{{
				Path: path,
				Edits: []workspacesdk.FileEdit{{
					Search:  "    value := 1\n",
					Replace: "    value := 2\n",
				}},
			}},
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		got, err := afero.ReadFile(fs, path)
		require.NoError(t, err)
		require.Contains(t, string(got), "value := 2")
	})

	//nolint:paralleltest // shares one mutable in-memory file with sibling subtests.
	t.Run("AssistantExactModeRejectsFuzzyCandidate", func(t *testing.T) {
		require.NoError(t, afero.WriteFile(fs, path, []byte(original), 0o644))
		rec := callEditFiles(t, api, workspacesdk.FileEditRequest{
			ExactOnly: true,
			Files: []workspacesdk.FileEdits{{
				Path: path,
				Edits: []workspacesdk.FileEdit{{
					Search:  "    value := 1\n",
					Replace: "    value := 2\n",
				}},
			}},
		})
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

		var sdkErr codersdk.Error
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &sdkErr))
		require.ErrorContains(t, &sdkErr, "exact match not found")
		require.ErrorContains(t, &sdkErr, "indentation_tolerant")
		require.ErrorContains(t, &sdkErr, "\tvalue := 1")
		require.ErrorContains(t, &sdkErr, "Use the exact candidate text above and retry")

		got, err := afero.ReadFile(fs, path)
		require.NoError(t, err)
		require.Equal(t, original, string(got), "exact-only failure must not modify the file")
	})

	//nolint:paralleltest // shares one mutable in-memory file with sibling subtests.
	t.Run("AssistantExactModeAppliesExactMatchAndDiff", func(t *testing.T) {
		require.NoError(t, afero.WriteFile(fs, path, []byte(original), 0o644))
		rec := callEditFiles(t, api, workspacesdk.FileEditRequest{
			ExactOnly:   true,
			IncludeDiff: true,
			Files: []workspacesdk.FileEdits{{
				Path: path,
				Edits: []workspacesdk.FileEdit{{
					Search:  original,
					Replace: "\tvalue := 3\n",
				}},
			}},
		})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var response workspacesdk.FileEditResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		require.Len(t, response.Files, 1)
		require.Contains(t, response.Files[0].Diff, "-\tvalue := 1")
		require.Contains(t, response.Files[0].Diff, "+\tvalue := 3")
		require.Len(t, response.Files[0].Edits, 1)
		require.Equal(t, "exact", response.Files[0].Edits[0].MatchMode)

		got, err := afero.ReadFile(fs, path)
		require.NoError(t, err)
		require.Equal(t, "\tvalue := 3\n", string(got))
	})
}
