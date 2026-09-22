package agentfiles_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/agent/agentchat"
	"github.com/coder/coder/v2/agent/agentfiles"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

func phase2FilesAPI(t *testing.T, fs afero.Fs) (*agentfiles.API, http.Handler) {
	t.Helper()
	logger := slogtest.Make(t, &slogtest.Options{IgnoreErrors: true}).Leveled(slog.LevelDebug)
	api := agentfiles.NewAPI(logger, fs, nil)
	return api, agentchat.Middleware(api.Routes())
}

func phase2JSONRequest(t *testing.T, handler http.Handler, method, path string, body any, headers ...http.Header) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitLong)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, method, path, reader)
	for _, header := range headers {
		for key, values := range header {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestPhase2ExpectedReplacements(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, "/edit.txt", []byte("alpha alpha\n"), 0o644))
	api, handler := phase2FilesAPI(t, fs)

	expectedZero := 0
	invalid := workspacesdk.FileEditRequest{Files: []workspacesdk.FileEdits{{
		Path: "/edit.txt",
		Edits: []workspacesdk.FileEdit{{
			Search:               "not-present",
			Replace:              "beta",
			ExpectedReplacements: &expectedZero,
		}},
	}}}
	w := phase2JSONRequest(t, handler, http.MethodPost, "/edit-files", invalid)
	require.Equal(t, http.StatusBadRequest, w.Code)
	var invalidResp codersdk.Response
	require.NoError(t, json.NewDecoder(w.Body).Decode(&invalidResp))
	require.Contains(t, invalidResp.Message, "expected_replacements must be at least 1")

	expectedThree := 3
	bad := workspacesdk.FileEditRequest{Files: []workspacesdk.FileEdits{{
		Path: "/edit.txt",
		Edits: []workspacesdk.FileEdit{{
			Search:               "alpha",
			Replace:              "beta",
			ReplaceAll:           true,
			ExpectedReplacements: &expectedThree,
		}},
	}}}
	w = phase2JSONRequest(t, handler, http.MethodPost, "/edit-files", bad)
	require.Equal(t, http.StatusBadRequest, w.Code)
	var errResp codersdk.Response
	require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
	require.Contains(t, errResp.Message, "matched 2 occurrences")
	content, err := afero.ReadFile(fs, "/edit.txt")
	require.NoError(t, err)
	require.Equal(t, "alpha alpha\n", string(content), "failed precondition must not write the file")

	expectedTwo := 2
	resp := runEditFiles(t, api, workspacesdk.FileEditRequest{
		IncludeDiff: true,
		Files: []workspacesdk.FileEdits{{
			Path: "/edit.txt",
			Edits: []workspacesdk.FileEdit{{
				Search:               "alpha",
				Replace:              "beta",
				ReplaceAll:           true,
				ExpectedReplacements: &expectedTwo,
			}},
		}},
	})
	require.Len(t, resp.Files, 1)
	require.Len(t, resp.Files[0].Edits, 1)
	require.Equal(t, "exact", resp.Files[0].Edits[0].MatchMode)
	require.Equal(t, 2, resp.Files[0].Edits[0].ReplacementCount)
	require.Equal(t, &expectedTwo, resp.Files[0].Edits[0].ExpectedReplacements)
	content, err = afero.ReadFile(fs, "/edit.txt")
	require.NoError(t, err)
	require.Equal(t, "beta beta\n", string(content))
}

func TestPhase2SearchPaginationAndChatIsolation(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/root/.hidden", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/root/a.txt", []byte("first needle here\n"), 0o644))
	require.NoError(t, afero.WriteFile(fs, "/root/b.txt", []byte("second needle here\n"), 0o644))
	require.NoError(t, afero.WriteFile(fs, "/root/.hidden/c.txt", []byte("hidden needle\n"), 0o644))
	_, handler := phase2FilesAPI(t, fs)

	chatA := uuid.New().String()
	headerA := http.Header{workspacesdk.CoderChatIDHeader: {chatA}}
	w := phase2JSONRequest(t, handler, http.MethodPost, "/search/start", workspacesdk.SearchStartRequest{
		Root:  "/root",
		Query: "needle",
		Mode:  "content",
	}, headerA)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var started workspacesdk.SearchStartResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&started))
	require.NotEmpty(t, started.ID)

	var first workspacesdk.SearchResultsResponse
	deadline := time.Now().Add(testutil.WaitLong)
	for {
		w = phase2JSONRequest(t, handler, http.MethodGet, "/search/"+started.ID+"/results?cursor=0&limit=1", nil, headerA)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		require.NoError(t, json.NewDecoder(w.Body).Decode(&first))
		if first.Search.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for search completion")
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Equal(t, "complete", first.Search.Status)
	require.Equal(t, 2, first.Search.ResultCount, "hidden path should be excluded by default")
	require.Len(t, first.Results, 1)
	require.NotNil(t, first.NextCursor)

	w = phase2JSONRequest(t, handler, http.MethodGet, "/search/"+started.ID+"/results?cursor=1&limit=10", nil, headerA)
	require.Equal(t, http.StatusOK, w.Code)
	var second workspacesdk.SearchResultsResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&second))
	require.Len(t, second.Results, 1)
	require.Nil(t, second.NextCursor)

	chatB := uuid.New().String()
	headerB := http.Header{workspacesdk.CoderChatIDHeader: {chatB}}
	w = phase2JSONRequest(t, handler, http.MethodGet, "/search/"+started.ID+"/results?cursor=0&limit=10", nil, headerB)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestPhase2SearchHasNoHiddenFileOrPreviewLimit(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/root", 0o755))

	largeFile := []byte("needle-in-large-file\n" + strings.Repeat("x", (2<<20)+1))
	require.NoError(t, afero.WriteFile(fs, "/root/large.txt", largeFile, 0o644))

	longPreview := strings.Repeat("p", 600) + " needle-in-long-preview"
	require.NoError(t, afero.WriteFile(fs, "/root/preview.txt", []byte(longPreview+"\n"), 0o644))

	_, handler := phase2FilesAPI(t, fs)
	w := phase2JSONRequest(t, handler, http.MethodPost, "/search/start", workspacesdk.SearchStartRequest{
		Root:  "/root",
		Query: "needle",
		Mode:  "content",
	})
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var started workspacesdk.SearchStartResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&started))

	var result workspacesdk.SearchResultsResponse
	deadline := time.Now().Add(testutil.WaitLong)
	for {
		w = phase2JSONRequest(t, handler, http.MethodGet, "/search/"+started.ID+"/results?cursor=0", nil)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
		if result.Search.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for unbounded search completion")
		}
		time.Sleep(10 * time.Millisecond)
	}

	require.Equal(t, "complete", result.Search.Status)
	require.Len(t, result.Results, 2)
	require.Equal(t, "/root/large.txt", result.Results[0].Path)
	require.Equal(t, "needle-in-large-file", result.Results[0].Text)
	require.Equal(t, "/root/preview.txt", result.Results[1].Path)
	require.Equal(t, longPreview, result.Results[1].Text, "search result text must not be silently preview-truncated")
}

func TestPhase2ListDirectoryV2(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/root/dir", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/root/a.txt", []byte("a"), 0o644))
	require.NoError(t, afero.WriteFile(fs, "/root/dir/b.txt", []byte("b"), 0o644))
	require.NoError(t, afero.WriteFile(fs, "/root/.hidden", []byte("hidden"), 0o644))
	_, handler := phase2FilesAPI(t, fs)

	w := phase2JSONRequest(t, handler, http.MethodPost, "/list-directory-v2", workspacesdk.ListDirectoryRequest{
		Path:  "/root",
		Depth: 2,
		Limit: 2,
	})
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var first workspacesdk.ListDirectoryResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&first))
	require.Len(t, first.Entries, 2)
	require.Equal(t, "/root/a.txt", first.Entries[0].Path)
	require.Equal(t, "/root/dir", first.Entries[1].Path)
	require.True(t, first.Entries[1].IsDir)
	require.True(t, first.HasMore)
	require.NotNil(t, first.NextCursor)
	require.Equal(t, 2, *first.NextCursor)

	w = phase2JSONRequest(t, handler, http.MethodPost, "/list-directory-v2", workspacesdk.ListDirectoryRequest{
		Path:   "/root",
		Depth:  2,
		Cursor: *first.NextCursor,
		Limit:  2,
	})
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var second workspacesdk.ListDirectoryResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&second))
	require.Len(t, second.Entries, 1)
	require.Equal(t, "/root/dir/b.txt", second.Entries[0].Path)
	require.False(t, second.HasMore)
	require.Nil(t, second.NextCursor)

	// The public MCP path uses a stable lexical continuation key rather than the
	// legacy numeric cursor. It must produce the same continuation even if the
	// caller does not know or preserve the numeric position.
	w = phase2JSONRequest(t, handler, http.MethodPost, "/list-directory-v2", workspacesdk.ListDirectoryRequest{
		Path:      "/root",
		Depth:     2,
		AfterPath: "dir",
		Limit:     2,
	})
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var lexical workspacesdk.ListDirectoryResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&lexical))
	require.Len(t, lexical.Entries, 1)
	require.Equal(t, "/root/dir/b.txt", lexical.Entries[0].Path)
	require.False(t, lexical.HasMore)
	require.Nil(t, lexical.NextCursor)
}

func TestPhase2ListDirectoryHasNoHiddenTraversalCap(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/root", 0o755))
	for i := 0; i < 5001; i++ {
		require.NoError(t, afero.WriteFile(fs, fmt.Sprintf("/root/file-%04d", i), nil, 0o644))
	}
	_, handler := phase2FilesAPI(t, fs)

	w := phase2JSONRequest(t, handler, http.MethodPost, "/list-directory-v2", workspacesdk.ListDirectoryRequest{
		Path:  "/root",
		Depth: 1,
	})
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var result workspacesdk.ListDirectoryResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&result))
	require.Len(t, result.Entries, 5001)
	require.Nil(t, result.NextCursor, "omitted limit must not manufacture a pagination boundary")
}

func TestPhase2MoveFileDanglingSymlinkOverwriteGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation can require elevated privileges on Windows")
	}
	t.Parallel()

	root := t.TempDir()
	fs := afero.NewOsFs()
	_, handler := phase2FilesAPI(t, fs)
	source := filepath.Join(root, "source.txt")
	dest := filepath.Join(root, "dest.txt")
	missingTarget := filepath.Join(root, "missing-target")
	require.NoError(t, os.WriteFile(source, []byte("payload"), 0o600))
	require.NoError(t, os.Symlink(missingTarget, dest))

	w := phase2JSONRequest(t, handler, http.MethodGet, "/file-info?path="+dest, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var info workspacesdk.WorkspaceFileInfo
	require.NoError(t, json.NewDecoder(w.Body).Decode(&info))
	require.True(t, info.IsSymlink)

	w = phase2JSONRequest(t, handler, http.MethodPost, "/move-file", workspacesdk.MoveFileRequest{
		Source: source,
		Dest:   dest,
	})
	require.Equal(t, http.StatusConflict, w.Code, "overwrite=false must see a dangling destination symlink as existing")
	content, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, "payload", string(content))
	linkInfo, err := os.Lstat(dest)
	require.NoError(t, err)
	require.NotZero(t, linkInfo.Mode()&os.ModeSymlink)

	w = phase2JSONRequest(t, handler, http.MethodPost, "/move-file", workspacesdk.MoveFileRequest{
		Source:    source,
		Dest:      dest,
		Overwrite: true,
	})
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	content, err = os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, "payload", string(content))
	linkInfo, err = os.Lstat(dest)
	require.NoError(t, err)
	require.Zero(t, linkInfo.Mode()&os.ModeSymlink)
}

func TestPhase2MoveFileRejectsNonEmptyDestinationBeforeMutation(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/root/source-dir", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/root/source-dir/source.txt", []byte("source"), 0o644))
	require.NoError(t, fs.MkdirAll("/root/dest", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/root/dest/existing.txt", []byte("keep"), 0o644))
	_, handler := phase2FilesAPI(t, fs)

	w := phase2JSONRequest(t, handler, http.MethodPost, "/move-file", workspacesdk.MoveFileRequest{
		Source:    "/root/source-dir/source.txt",
		Dest:      "/root/dest",
		Overwrite: true,
	})
	require.Equal(t, http.StatusConflict, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), "destination directory is not empty")

	source, err := afero.ReadFile(fs, "/root/source-dir/source.txt")
	require.NoError(t, err)
	require.Equal(t, "source", string(source))
	existing, err := afero.ReadFile(fs, "/root/dest/existing.txt")
	require.NoError(t, err)
	require.Equal(t, "keep", string(existing))
}

func TestPhase2MoveFileSourceRenameFailureRestoresDestination(t *testing.T) {
	t.Parallel()

	base := afero.NewMemMapFs()
	destRenameCalls := 0
	fs := newTestFs(base, func(call, path string) error {
		if call == "rename" && path == "/root/dest.txt" {
			destRenameCalls++
			if destRenameCalls == 1 {
				return syscall.EXDEV
			}
		}
		return nil
	})
	require.NoError(t, fs.MkdirAll("/root", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/root/source.txt", []byte("new"), 0o644))
	require.NoError(t, afero.WriteFile(fs, "/root/dest.txt", []byte("old"), 0o600))
	_, handler := phase2FilesAPI(t, fs)

	w := phase2JSONRequest(t, handler, http.MethodPost, "/move-file", workspacesdk.MoveFileRequest{
		Source:    "/root/source.txt",
		Dest:      "/root/dest.txt",
		Overwrite: true,
	})
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), "move path")

	source, err := afero.ReadFile(base, "/root/source.txt")
	require.NoError(t, err)
	require.Equal(t, "new", string(source))
	dest, err := afero.ReadFile(base, "/root/dest.txt")
	require.NoError(t, err)
	require.Equal(t, "old", string(dest))

	entries, err := afero.ReadDir(base, "/root")
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), ".coder-backup.", "failed cross-filesystem-style rename must restore and clean destination backup")
	}
}

func TestPhase2MoveFileRollbackFailureReportsRecoveryState(t *testing.T) {
	t.Parallel()

	base := afero.NewMemMapFs()
	fs := newTestFs(base, func(call, path string) error {
		if call == "rename" && path == "/root/dest.txt" {
			return xerrors.New("injected destination rename failure")
		}
		return nil
	})
	require.NoError(t, fs.MkdirAll("/root", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/root/source.txt", []byte("new"), 0o644))
	require.NoError(t, afero.WriteFile(fs, "/root/dest.txt", []byte("old"), 0o600))
	_, handler := phase2FilesAPI(t, fs)

	w := phase2JSONRequest(t, handler, http.MethodPost, "/move-file", workspacesdk.MoveFileRequest{
		Source:    "/root/source.txt",
		Dest:      "/root/dest.txt",
		Overwrite: true,
	})
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), "destination rollback also failed")
	require.Contains(t, w.Body.String(), "backup remains at")

	source, err := afero.ReadFile(base, "/root/source.txt")
	require.NoError(t, err)
	require.Equal(t, "new", string(source))
	_, err = base.Stat("/root/dest.txt")
	require.Error(t, err)

	entries, err := afero.ReadDir(base, "/root")
	require.NoError(t, err)
	var backupPath string
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".coder-backup.") {
			backupPath = "/root/" + entry.Name()
			break
		}
	}
	require.NotEmpty(t, backupPath)
	backup, err := afero.ReadFile(base, backupPath)
	require.NoError(t, err)
	require.Equal(t, "old", string(backup))
	require.Contains(t, w.Body.String(), backupPath)
}

func TestPhase2MoveFileCleanupFailureRollsBack(t *testing.T) {
	t.Parallel()

	base := afero.NewMemMapFs()
	fs := newTestFs(base, func(call, path string) error {
		if call == "remove" && strings.Contains(path, ".coder-backup.") {
			return xerrors.New("injected backup cleanup failure")
		}
		return nil
	})
	require.NoError(t, fs.MkdirAll("/root", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/root/source.txt", []byte("new"), 0o644))
	require.NoError(t, afero.WriteFile(fs, "/root/dest.txt", []byte("old"), 0o644))
	_, handler := phase2FilesAPI(t, fs)

	w := phase2JSONRequest(t, handler, http.MethodPost, "/move-file", workspacesdk.MoveFileRequest{
		Source:    "/root/source.txt",
		Dest:      "/root/dest.txt",
		Overwrite: true,
	})
	require.Equal(t, http.StatusInternalServerError, w.Code, "body: %s", w.Body.String())
	require.Contains(t, w.Body.String(), "move rolled back because destination backup cleanup failed")

	source, err := afero.ReadFile(fs, "/root/source.txt")
	require.NoError(t, err)
	require.Equal(t, "new", string(source))
	dest, err := afero.ReadFile(fs, "/root/dest.txt")
	require.NoError(t, err)
	require.Equal(t, "old", string(dest))

	entries, err := afero.ReadDir(base, "/root")
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), ".coder-backup.")
	}
}

func TestPhase2FileMetadataCreateAndMove(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	_, handler := phase2FilesAPI(t, fs)

	w := phase2JSONRequest(t, handler, http.MethodPost, "/create-directory", workspacesdk.CreateDirectoryRequest{
		Path:    "/root/nested",
		Parents: true,
	})
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, afero.WriteFile(fs, "/root/nested/source.txt", []byte("payload"), 0o640))

	w = phase2JSONRequest(t, handler, http.MethodGet, "/file-info?path=/root/nested/source.txt", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var info workspacesdk.WorkspaceFileInfo
	require.NoError(t, json.NewDecoder(w.Body).Decode(&info))
	require.Equal(t, int64(7), info.Size)
	require.False(t, info.IsDir)
	require.Contains(t, info.Mode, "rw")

	w = phase2JSONRequest(t, handler, http.MethodPost, "/move-file", workspacesdk.MoveFileRequest{
		Source: "/root/nested/source.txt",
		Dest:   "/root/nested/dest.txt",
	})
	require.Equal(t, http.StatusOK, w.Code)
	_, err := fs.Stat("/root/nested/source.txt")
	require.Error(t, err)
	content, err := afero.ReadFile(fs, "/root/nested/dest.txt")
	require.NoError(t, err)
	require.Equal(t, "payload", string(content))
}
