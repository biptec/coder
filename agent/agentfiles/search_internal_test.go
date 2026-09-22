package agentfiles

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func TestAppendSearchResultRetentionBudgetFailsExplicitly(t *testing.T) {
	t.Parallel()

	session := &searchSession{retainedBytes: searchResultRetentionBytesMax - 8}
	err := appendSearchResult(session, workspacesdk.SearchResult{
		Path: "/root/file.txt",
		Text: "needle",
	}, 0)
	require.ErrorIs(t, err, errSearchResource)
	require.Contains(t, err.Error(), "positive max_results")
	require.Empty(t, session.results)
	require.Equal(t, int64(searchResultRetentionBytesMax-8), session.retainedBytes)
}

func TestSearchContentSkipsFIFOWithoutOpening(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes use different filesystem semantics on Windows")
	}
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "regular.txt"), []byte("needle\n"), 0o600))
	pipePath := filepath.Join(root, "pipe")
	require.NoError(t, syscall.Mkfifo(pipePath, 0o600))

	manager := newSearchManager(afero.NewOsFs())
	matcher, err := newSearchMatcher("needle", false, true)
	require.NoError(t, err)
	session := &searchSession{info: workspacesdk.SearchSessionInfo{Status: "running"}, cancel: func() {}}

	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.run(context.Background(), session, workspacesdk.SearchStartRequest{
			Root:  root,
			Query: "needle",
			Mode:  "content",
		}, matcher, 0)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("content search blocked on FIFO instead of skipping the special file")
	}

	info := session.snapshot()
	require.Equal(t, "complete", info.Status)
	require.Equal(t, 1, info.ResultCount)
	require.Len(t, session.results, 1)
	require.Equal(t, filepath.Join(root, "regular.txt"), session.results[0].Path)
}

func TestSearchContentSkipsDeviceWithoutOpening(t *testing.T) {
	t.Parallel()

	const path = "/tmp/fake-device"
	fs := &specialStatFS{
		Fs:          afero.NewMemMapFs(),
		specialPath: path,
		specialMode: os.ModeDevice | os.ModeCharDevice,
	}
	manager := newSearchManager(fs)
	matcher, err := newSearchMatcher("needle", false, true)
	require.NoError(t, err)
	session := &searchSession{
		info:   workspacesdk.SearchSessionInfo{Status: "running"},
		cancel: func() {},
	}

	manager.run(context.Background(), session, workspacesdk.SearchStartRequest{
		Root:  path,
		Query: "needle",
		Mode:  "content",
	}, matcher, 0)

	info := session.snapshot()
	require.Equal(t, "complete", info.Status)
	require.Zero(t, info.ResultCount)
	require.Empty(t, session.results)
	require.False(t, fs.openCalled, "content search must skip device files before Open")
}

func TestSearchResourceBudgetBecomesExplicitErrorStatus(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/root", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/root/a.txt", []byte("needle\n"), 0o600))
	manager := newSearchManager(fs)
	matcher, err := newSearchMatcher("needle", false, true)
	require.NoError(t, err)
	session := &searchSession{
		info:          workspacesdk.SearchSessionInfo{Status: "running"},
		retainedBytes: searchResultRetentionBytesMax - 1,
		cancel:        func() {},
	}

	manager.run(context.Background(), session, workspacesdk.SearchStartRequest{
		Root:  "/root",
		Query: "needle",
		Mode:  "content",
	}, matcher, 0)

	info := session.snapshot()
	require.Equal(t, "error", info.Status)
	require.False(t, info.Truncated)
	require.Contains(t, info.Error, "retention safety budget")
	require.Contains(t, info.Error, "positive max_results")
	require.Empty(t, session.results)
}

func TestSearchPositiveMaxResultsIsDeterministicAndTruncated(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/root/sub", 0o755))
	for path, content := range map[string]string{
		"/root/z.txt":     "needle z\n",
		"/root/a.txt":     "needle a\n",
		"/root/sub/b.txt": "needle b\n",
	} {
		require.NoError(t, afero.WriteFile(fs, path, []byte(content), 0o600))
	}
	manager := newSearchManager(fs)
	matcher, err := newSearchMatcher("needle", false, true)
	require.NoError(t, err)
	session := &searchSession{info: workspacesdk.SearchSessionInfo{Status: "running"}, cancel: func() {}}

	manager.run(context.Background(), session, workspacesdk.SearchStartRequest{
		Root:  "/root",
		Query: "needle",
		Mode:  "content",
	}, matcher, 2)

	info := session.snapshot()
	require.Equal(t, "complete", info.Status)
	require.True(t, info.Truncated)
	require.Equal(t, 2, info.ResultCount)
	require.Len(t, session.results, 2)
	require.Equal(t, "/root/a.txt", session.results[0].Path)
	require.Equal(t, "/root/sub/b.txt", session.results[1].Path)
}

func TestSearchLiteralRegexCaseAndHiddenBehavior(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/root/.hidden", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/root/a.txt", []byte("Needle 123\nneedle 456\n"), 0o600))
	require.NoError(t, afero.WriteFile(fs, "/root/.hidden/b.txt", []byte("needle 789\n"), 0o600))

	run := func(query string, regex, caseSensitive, includeHidden bool) []workspacesdk.SearchResult {
		matcher, err := newSearchMatcher(query, regex, caseSensitive)
		require.NoError(t, err)
		manager := newSearchManager(fs)
		session := &searchSession{info: workspacesdk.SearchSessionInfo{Status: "running"}, cancel: func() {}}
		manager.run(context.Background(), session, workspacesdk.SearchStartRequest{
			Root:          "/root",
			Query:         query,
			Mode:          "content",
			Regex:         regex,
			CaseSensitive: caseSensitive,
			IncludeHidden: includeHidden,
		}, matcher, 0)
		require.Equal(t, "complete", session.snapshot().Status)
		return append([]workspacesdk.SearchResult(nil), session.results...)
	}

	insensitive := run("needle", false, false, false)
	require.Len(t, insensitive, 2)
	require.Equal(t, []int{1, 2}, []int{insensitive[0].Line, insensitive[1].Line})

	sensitive := run("needle", false, true, false)
	require.Len(t, sensitive, 1)
	require.Equal(t, 2, sensitive[0].Line)

	regexResults := run("Needle [0-9]+", true, true, false)
	require.Len(t, regexResults, 1)
	require.Equal(t, 1, regexResults[0].Line)

	withHidden := run("needle", false, false, true)
	require.Len(t, withHidden, 3)
	foundHidden := false
	for _, result := range withHidden {
		foundHidden = foundHidden || strings.Contains(result.Path, ".hidden")
	}
	require.True(t, foundHidden)
}

func TestSearchFileModeAndLifecycle(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/root/.hidden", 0o755))
	require.NoError(t, afero.WriteFile(fs, "/root/b.txt", nil, 0o600))
	require.NoError(t, afero.WriteFile(fs, "/root/a.txt", nil, 0o600))
	require.NoError(t, afero.WriteFile(fs, "/root/.hidden/c.txt", nil, 0o600))

	manager := newSearchManager(fs)
	matcher, err := newSearchMatcher(".txt", false, true)
	require.NoError(t, err)
	session := &searchSession{info: workspacesdk.SearchSessionInfo{Status: "running"}, cancel: func() {}}
	manager.run(context.Background(), session, workspacesdk.SearchStartRequest{
		Root:  "/root",
		Query: ".txt",
		Mode:  "files",
	}, matcher, 0)

	require.Equal(t, "complete", session.snapshot().Status)
	require.Equal(t, []string{"/root/a.txt", "/root/b.txt"}, []string{
		session.results[0].Path,
		session.results[1].Path,
	})

	canceled := false
	running := &searchSession{
		info:   workspacesdk.SearchSessionInfo{ID: "running", Status: "running"},
		cancel: func() { canceled = true },
	}
	manager.sessions["running"] = running
	require.NoError(t, manager.stop("running", ""))
	require.True(t, canceled, "stop must cancel a running search")

	completedAt := time.Now().Add(-searchSessionTTL - time.Minute).Unix()
	expired := &searchSession{
		info: workspacesdk.SearchSessionInfo{
			ID:          "expired",
			Status:      "complete",
			CompletedAt: &completedAt,
		},
		cancel: func() {},
	}
	manager.sessions["expired"] = expired
	_, ok := manager.get("expired", "")
	require.False(t, ok, "completed search sessions older than the retention TTL must expire")
}

func TestAppendSearchResultExplicitCountLimitRemainsDistinct(t *testing.T) {
	t.Parallel()

	session := &searchSession{
		results: []workspacesdk.SearchResult{{Path: "/root/first.txt"}},
	}
	err := appendSearchResult(session, workspacesdk.SearchResult{Path: "/root/second.txt"}, 1)
	require.ErrorIs(t, err, errSearchLimit)
	require.True(t, session.info.Truncated)
	require.Len(t, session.results, 1)
}
