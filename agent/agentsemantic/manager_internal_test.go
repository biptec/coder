package agentsemantic

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/agent/agentexec"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

func newLiveSemanticManager(t *testing.T) *Manager {
	t.Helper()
	manager := NewManager(
		context.Background(),
		slogtest.Make(t, nil),
		agentexec.DefaultExecer,
		nil,
	)
	t.Cleanup(func() {
		require.NoError(t, manager.Close())
	})
	return manager
}

func newLiveGoplsManager(t *testing.T) *Manager {
	t.Helper()
	return newLiveSemanticManager(t)
}

func writeSemanticFixture(t *testing.T) (root, path string) {
	t.Helper()
	root = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/semantic\n\ngo 1.26\n"), 0o600))
	path = filepath.Join(root, "semantic.go")
	source := `package semantic

type Greeter interface {
	Greet() string
}

type English struct{}

type Config struct {
	Enabled bool
}

const Answer = 42

func (English) Greet() string {
	return "hello"
}

func UseGreeter(g Greeter) string {
	return g.Greet()
}

func Target(value int) int {
	return value + 1
}

func Caller() int {
	return Target(41)
}

// Target(99) is text, not a semantic reference.
var TargetText = "Target(100)"
`
	require.NoError(t, os.WriteFile(path, []byte(source), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "other.go"), []byte(`package semantic

func OtherCaller() int {
	return Target(1)
}
`), 0o600))
	return root, path
}

func TestNotifyPathsChangedDoesNotStartBackend(t *testing.T) {
	t.Parallel()

	manager := newLiveGoplsManager(t)
	path := filepath.Join(t.TempDir(), "untracked.go")
	require.NoError(t, os.WriteFile(path, []byte("package untracked\n"), 0o600))

	require.NoError(t, manager.NotifyPathsChanged(context.Background(), path))

	manager.mu.Lock()
	sessionCount := len(manager.sessions)
	manager.mu.Unlock()
	require.Zero(t, sessionCount)
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestNotifyPathsChangedRefreshesTrackedDocument(t *testing.T) {
	if _, err := os.Stat(trustedGoplsPath); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	_, path := writeSemanticFixture(t)
	manager := newLiveGoplsManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	symbols, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "Target", Match: "exact", Kinds: []string{"function"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, symbols.Symbols, 1)

	projectRoot, err := goProjectRootForFile(path)
	require.NoError(t, err)
	key := languageGo + ":" + projectRoot
	manager.mu.Lock()
	session := manager.sessions[key]
	manager.mu.Unlock()
	require.NotNil(t, session)

	session.docsMu.Lock()
	before := session.docs[path]
	session.docsMu.Unlock()
	require.Positive(t, before.version)

	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	updated := strings.Replace(string(contents), "return value + 1", "return value + 2", 1)
	require.NotEqual(t, string(contents), updated)
	require.NoError(t, os.WriteFile(path, []byte(updated), 0o600))

	require.NoError(t, manager.NotifyPathsChanged(ctx, path))

	session.docsMu.Lock()
	after := session.docs[path]
	session.docsMu.Unlock()
	require.Greater(t, after.version, before.version)
	require.NotEqual(t, before.hash, after.hash)

	require.NoError(t, os.Remove(path))
	require.NoError(t, manager.NotifyPathsChanged(ctx, path))

	session.docsMu.Lock()
	_, tracked := session.docs[path]
	session.docsMu.Unlock()
	require.False(t, tracked)
}

func TestDecodeDocumentSymbolResponseForms(t *testing.T) {
	t.Parallel()

	hierarchicalRaw := json.RawMessage(`[
		{"name":"Thing","kind":12,"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}},"selectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}}}
	]`)
	hierarchical, flat, isHierarchical, err := decodeDocumentSymbolResponse(hierarchicalRaw)
	require.NoError(t, err)
	require.True(t, isHierarchical)
	require.Len(t, hierarchical, 1)
	require.Empty(t, flat)

	flatRaw := json.RawMessage(`[
		{"name":"Thing","kind":12,"location":{"uri":"file:///tmp/thing.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}}}}
	]`)
	hierarchical, flat, isHierarchical, err = decodeDocumentSymbolResponse(flatRaw)
	require.NoError(t, err)
	require.False(t, isHierarchical)
	require.Empty(t, hierarchical)
	require.Len(t, flat, 1)
}

func TestReadSemanticSourceRejectsOversizeFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "huge.go")
	file, err := os.Create(path)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(maxSemanticSourceBytes+1))
	require.NoError(t, file.Close())

	_, err = readSemanticSource(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeding internal safety limit")
}

func TestFileURIRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "space Ж", "file.go")
	uri := pathToFileURI(path)
	roundTrip, err := fileURIToPath(uri)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(path), roundTrip)
}

func TestGoProjectRootResolution(t *testing.T) {
	t.Parallel()
	t.Run("applicable go.work wins", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		moduleDir := filepath.Join(root, "module")
		require.NoError(t, os.MkdirAll(moduleDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26\n\nuse ./module\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte("module example.com/module\n\ngo 1.26\n"), 0o600))
		path := filepath.Join(moduleDir, "file.go")
		require.NoError(t, os.WriteFile(path, []byte("package module\n"), 0o600))

		got, err := goProjectRootForFile(path)
		require.NoError(t, err)
		require.Equal(t, root, got)
	})

	t.Run("unlisted module ignores enclosing go.work", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		usedDir := filepath.Join(root, "used")
		unlistedDir := filepath.Join(root, "unlisted")
		require.NoError(t, os.MkdirAll(usedDir, 0o755))
		require.NoError(t, os.MkdirAll(unlistedDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26\n\nuse ./used\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(usedDir, "go.mod"), []byte("module example.com/used\n\ngo 1.26\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(unlistedDir, "go.mod"), []byte("module example.com/unlisted\n\ngo 1.26\n"), 0o600))
		path := filepath.Join(unlistedDir, "file.go")
		require.NoError(t, os.WriteFile(path, []byte("package unlisted\n"), 0o600))

		got, err := goProjectRootForFile(path)
		require.NoError(t, err)
		require.Equal(t, unlistedDir, got)
	})

	t.Run("nearest nested go.mod wins without go.work", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		nested := filepath.Join(root, "nested")
		require.NoError(t, os.MkdirAll(nested, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/root\n\ngo 1.26\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module example.com/nested\n\ngo 1.26\n"), 0o600))
		path := filepath.Join(nested, "file.go")
		require.NoError(t, os.WriteFile(path, []byte("package nested\n"), 0o600))

		got, err := goProjectRootForFile(path)
		require.NoError(t, err)
		require.Equal(t, nested, got)
	})

	t.Run("ad hoc file falls back to containing directory", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		path := filepath.Join(root, "file.go")
		require.NoError(t, os.WriteFile(path, []byte("package adhoc\n"), 0o600))

		got, err := goProjectRootForFile(path)
		require.NoError(t, err)
		require.Equal(t, root, got)
	})

	t.Run("directory combines go.work with unlisted nested module", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		usedDir := filepath.Join(root, "used")
		unlistedDir := filepath.Join(root, "unlisted")
		require.NoError(t, os.MkdirAll(usedDir, 0o755))
		require.NoError(t, os.MkdirAll(unlistedDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.26\n\nuse ./used\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(usedDir, "go.mod"), []byte("module example.com/used\n\ngo 1.26\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(unlistedDir, "go.mod"), []byte("module example.com/unlisted\n\ngo 1.26\n"), 0o600))

		roots, coverage, err := goProjectRootsForDirectory(root)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{root, unlistedDir}, roots)
		require.Equal(t, coverageUnknown, coverage.Status)
	})
}

func TestPositionEncodingRoundTrip(t *testing.T) {
	t.Parallel()

	line := "a🙂Ж\txyz"
	tests := []string{"utf-8", "utf-16", "utf-32"}
	for _, encoding := range tests {
		encoding := encoding
		t.Run(encoding, func(t *testing.T) {
			t.Parallel()
			for column := 1; column <= 8; column++ {
				lspColumn, err := publicColumnToLSP(line, column, encoding)
				require.NoError(t, err)
				publicColumn, err := lspColumnToPublic(line, lspColumn, encoding)
				require.NoError(t, err)
				require.Equal(t, column, publicColumn)
			}
		})
	}
}

func TestSymbolMatchRankPrefersStrongerMatches(t *testing.T) {
	t.Parallel()

	require.Less(t, symbolMatchRank("Target", "Target"), symbolMatchRank("TargetExtra", "Target"))
	require.Less(t, symbolMatchRank("TargetExtra", "Target"), symbolMatchRank("ATarget", "Target"))
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestManagerGoSemanticFlow(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/gopls"); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	root, path := writeSemanticFixture(t)
	manager := newLiveGoplsManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	targetResponse, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: root, Query: "Target", Match: "exact", Limit: 20, ContextLines: 1,
	})
	require.NoError(t, err)
	require.Len(t, targetResponse.Symbols, 1)
	require.Equal(t, "function", targetResponse.Symbols[0].Kind)
	require.Equal(t, path, targetResponse.Symbols[0].Path)
	require.NotNil(t, targetResponse.Symbols[0].Context)
	require.Equal(t, 1, targetResponse.ReturnedCount)

	missing, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "DefinitelyMissingSymbol", Match: "exact", Limit: 20,
	})
	require.NoError(t, err)
	require.NotNil(t, missing.Symbols)
	require.Empty(t, missing.Symbols)
	missingJSON, err := json.Marshal(missing)
	require.NoError(t, err)
	require.Contains(t, string(missingJSON), `"symbols":[]`)

	prefix, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: root, Query: "Targ", Match: "prefix", Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, prefix.Symbols, 1)
	require.Equal(t, 2, prefix.ObservedCount)
	require.True(t, prefix.Truncated)

	unlimitedPrefix, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: root, Query: "Targ", Match: "prefix", Limit: 0,
	})
	require.NoError(t, err)
	require.Len(t, unlimitedPrefix.Symbols, 2)
	require.Equal(t, 2, unlimitedPrefix.ObservedCount)
	require.False(t, unlimitedPrefix.Truncated)
	require.LessOrEqual(t, unlimitedPrefix.Symbols[0].Name, unlimitedPrefix.Symbols[1].Name)

	functionOnly, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: root, Query: "Targ", Match: "prefix", Kinds: []string{"function"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, functionOnly.Symbols, 1)
	require.Equal(t, "Target", functionOnly.Symbols[0].Name)

	substring, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "arge", Match: "substring", Kinds: []string{"function"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, substring.Symbols, 1)
	require.Equal(t, "Target", substring.Symbols[0].Name)

	useGreeter, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "UseGreeter", Match: "exact", Kinds: []string{"function"}, Limit: 20, ContextLines: 1,
	})
	require.NoError(t, err)
	require.Len(t, useGreeter.Symbols, 1)
	require.NotNil(t, useGreeter.Symbols[0].Context)
	require.Equal(t, useGreeter.Symbols[0].SelectionRange.Start.Line-1, useGreeter.Symbols[0].Context.StartLine)
	require.Equal(t, useGreeter.Symbols[0].SelectionRange.End.Line+1, useGreeter.Symbols[0].Context.EndLine)

	structSymbol, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "Config", Match: "exact", Kinds: []string{"struct"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, structSymbol.Symbols, 1)

	fieldSymbol, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "Enabled", Match: "exact", Kinds: []string{"field"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, fieldSymbol.Symbols, 1)

	constantSymbol, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "Answer", Match: "exact", Kinds: []string{"constant"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, constantSymbol.Symbols, 1)

	references, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: targetResponse.Symbols[0].Locator,
		Limit:  20,
	})
	require.NoError(t, err)
	require.Len(t, references.References, 2)

	greeter, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "Greeter", Match: "exact", Kinds: []string{"interface"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, greeter.Symbols, 1)

	implementations, err := manager.FindImplementations(ctx, workspacesdk.SemanticFindImplementationsRequest{
		Target: greeter.Symbols[0].Locator,
		Limit:  20,
	})
	require.NoError(t, err)
	require.NotEmpty(t, implementations.Implementations)
	require.Equal(t, path, implementations.Implementations[0].Path)

	manager.mu.Lock()
	sessionCount := len(manager.sessions)
	manager.mu.Unlock()
	require.Equal(t, 1, sessionCount, "semantic requests in one Go module should reuse one gopls session")
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestManagerDirectorySymbolSearchContinuesAfterRootFailure(t *testing.T) {
	if _, err := os.Stat(trustedGoplsPath); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	root := t.TempDir()
	badRoot := filepath.Join(root, "a_bad")
	goodRoot := filepath.Join(root, "b_good")
	require.NoError(t, os.MkdirAll(badRoot, 0o755))
	require.NoError(t, os.MkdirAll(goodRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(badRoot, "go.mod"), []byte("module example.com/bad\n\ngo 1.26\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(badRoot, "bad.go"), []byte("package bad\ntype Bad struct{}\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(goodRoot, "go.mod"), []byte("module example.com/good\n\ngo 1.26\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(goodRoot, "good.go"), []byte("package good\nfunc GoodTarget() {}\n"), 0o600))

	manager := newLiveGoplsManager(t)
	fakeClient := &lspClient{
		done: make(chan struct{}),
		capabilities: lspCapabilities{
			positionEncoding: "utf-16",
			workspaceSymbols: false,
		},
	}
	badKey := languageGo + ":" + badRoot
	manager.mu.Lock()
	manager.sessions[badKey] = &semanticSession{root: badRoot, client: fakeClient}
	manager.mu.Unlock()
	t.Cleanup(func() {
		manager.mu.Lock()
		delete(manager.sessions, badKey)
		manager.mu.Unlock()
	})

	ctx := testutil.Context(t, testutil.WaitLong)
	result, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: root, Query: "GoodTarget", Match: "exact", Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, result.Symbols, 1)
	require.Equal(t, "GoodTarget", result.Symbols[0].Name)
	require.Equal(t, coveragePartial, result.Coverage.Status)
	require.Contains(t, result.Coverage.Reason, "does not support workspace symbol lookup")
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestManagerConcurrentStartupReusesBackend(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/gopls"); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	_, path := writeSemanticFixture(t)
	manager := newLiveGoplsManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	const callers = 4
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
				Root: path, Query: "Target", Match: "exact", Limit: 20,
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	manager.mu.Lock()
	sessionCount := len(manager.sessions)
	manager.mu.Unlock()
	require.Equal(t, 1, sessionCount)
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestManagerRestartsCrashedBackend(t *testing.T) {
	if _, err := os.Stat(trustedGoplsPath); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	_, path := writeSemanticFixture(t)
	manager := newLiveGoplsManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	_, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "Target", Match: "exact", Limit: 20,
	})
	require.NoError(t, err)

	manager.mu.Lock()
	var first *semanticSession
	for _, session := range manager.sessions {
		first = session
		break
	}
	manager.mu.Unlock()
	require.NotNil(t, first)
	require.Equal(t, trustedGoplsPath, first.client.cmd.Path)
	firstPID := first.client.cmd.Process.Pid

	require.NoError(t, first.client.cmd.Process.Kill())
	select {
	case <-first.client.done:
	case <-time.After(5 * time.Second):
		t.Fatal("gopls did not report exit after kill")
	}

	_, err = manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "Target", Match: "exact", Limit: 20,
	})
	require.NoError(t, err)

	manager.mu.Lock()
	var second *semanticSession
	for _, session := range manager.sessions {
		second = session
		break
	}
	manager.mu.Unlock()
	require.NotNil(t, second)
	require.NotSame(t, first, second)
	require.NotEqual(t, firstPID, second.client.cmd.Process.Pid)
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestManagerReferenceScopeLimitAndDeclaration(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/gopls"); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	_, path := writeSemanticFixture(t)
	manager := newLiveGoplsManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	target, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "Target", Match: "exact", Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, target.Symbols, 1)

	all, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: target.Symbols[0].Locator, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, all.References, 2)

	limited, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: target.Symbols[0].Locator, Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, limited.References, 1)
	require.Equal(t, 2, limited.ObservedCount)
	require.True(t, limited.Truncated)

	scoped, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: target.Symbols[0].Locator, ScopePath: path, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, scoped.References, 1)
	require.Equal(t, path, scoped.References[0].Path)

	directoryScoped, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: target.Symbols[0].Locator, ScopePath: filepath.Dir(path), Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, directoryScoped.References, 2)

	withDeclaration, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: target.Symbols[0].Locator, IncludeDeclaration: true, Limit: 20,
	})
	require.NoError(t, err)
	require.Greater(t, len(withDeclaration.References), len(all.References))
	foundDeclaration := false
	for _, reference := range withDeclaration.References {
		if reference.Path == path &&
			reference.Range.Start.Line == target.Symbols[0].SelectionRange.Start.Line &&
			reference.Range.Start.Column == target.Symbols[0].SelectionRange.Start.Column {
			foundDeclaration = true
			break
		}
	}
	require.True(t, foundDeclaration)
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestManagerDuplicateMethodNamesStaySemantic(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/gopls"); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/methods\n\ngo 1.26\n"), 0o600))
	path := filepath.Join(root, "methods.go")
	require.NoError(t, os.WriteFile(path, []byte(`package methods

type Alpha struct{}
type Beta struct{}

func (Alpha) Ping() {}
func (Beta) Ping() {}

func UseAlpha(value Alpha) {
	value.Ping()
}

func UseBeta(value Beta) {
	value.Ping()
}
`), 0o600))

	manager := newLiveGoplsManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	symbols, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "Ping", Match: "exact", Kinds: []string{"method"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, symbols.Symbols, 2)

	for _, symbol := range symbols.Symbols {
		references, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
			Target: symbol.Locator, Limit: 20,
		})
		require.NoError(t, err)
		require.Len(t, references.References, 1, "same-named methods on different receiver types must not mix")
	}
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestManagerImplementationsEmptyAndScoped(t *testing.T) {
	if _, err := os.Stat(trustedGoplsPath); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/implementations\n\ngo 1.26\n"), 0o600))
	interfacePath := filepath.Join(root, "interfaces.go")
	alphaPath := filepath.Join(root, "alpha.go")
	betaPath := filepath.Join(root, "beta.go")
	require.NoError(t, os.WriteFile(interfacePath, []byte("package implementations\n\ntype Runner interface {\n\tRun()\n}\n\ntype Unused interface {\n\tNever()\n}\n"), 0o600))
	require.NoError(t, os.WriteFile(alphaPath, []byte("package implementations\n\ntype Alpha struct{}\n\nfunc (Alpha) Run() {}\n"), 0o600))
	require.NoError(t, os.WriteFile(betaPath, []byte("package implementations\n\ntype Beta struct{}\n\nfunc (Beta) Run() {}\n"), 0o600))

	manager := newLiveGoplsManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	runner, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: interfacePath, Query: "Runner", Match: "exact", Kinds: []string{"interface"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, runner.Symbols, 1)

	all, err := manager.FindImplementations(ctx, workspacesdk.SemanticFindImplementationsRequest{
		Target: runner.Symbols[0].Locator,
		Limit:  20,
	})
	require.NoError(t, err)
	require.Len(t, all.Implementations, 2)

	runMethod, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: interfacePath, Query: "Run", Match: "exact", Kinds: []string{"method"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, runMethod.Symbols, 1)

	methodImplementations, err := manager.FindImplementations(ctx, workspacesdk.SemanticFindImplementationsRequest{
		Target: runMethod.Symbols[0].Locator,
		Limit:  20,
	})
	require.NoError(t, err)
	require.Len(t, methodImplementations.Implementations, 2)
	for _, implementation := range methodImplementations.Implementations {
		require.NotEqual(t, interfacePath, implementation.Path, "queried interface method must not be returned as its own implementation")
	}

	limited, err := manager.FindImplementations(ctx, workspacesdk.SemanticFindImplementationsRequest{
		Target: runner.Symbols[0].Locator,
		Limit:  1,
	})
	require.NoError(t, err)
	require.Len(t, limited.Implementations, 1)
	require.Equal(t, 2, limited.ObservedCount)
	require.True(t, limited.Truncated)

	scoped, err := manager.FindImplementations(ctx, workspacesdk.SemanticFindImplementationsRequest{
		Target:    runner.Symbols[0].Locator,
		ScopePath: alphaPath,
		Limit:     20,
	})
	require.NoError(t, err)
	require.Len(t, scoped.Implementations, 1)
	require.Equal(t, alphaPath, scoped.Implementations[0].Path)

	unused, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: interfacePath, Query: "Unused", Match: "exact", Kinds: []string{"interface"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, unused.Symbols, 1)

	none, err := manager.FindImplementations(ctx, workspacesdk.SemanticFindImplementationsRequest{
		Target: unused.Symbols[0].Locator,
		Limit:  20,
	})
	require.NoError(t, err)
	require.Empty(t, none.Implementations)
	require.Zero(t, none.ObservedCount)
	require.False(t, none.Truncated)
}

func TestManagerImplementationsCapabilityUnsupportedIsExplicit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/unsupported\n\ngo 1.26\n"), 0o600))
	path := filepath.Join(root, "types.go")
	require.NoError(t, os.WriteFile(path, []byte("package unsupported\ntype Runner interface { Run() }\n"), 0o600))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &lspClient{
		done: make(chan struct{}),
		capabilities: lspCapabilities{
			positionEncoding: "utf-16",
			implementations:  false,
		},
	}
	manager := &Manager{
		ctx:      ctx,
		cancel:   cancel,
		sessions: map[string]*semanticSession{languageGo + ":" + root: {root: root, client: client}},
	}

	_, err := manager.FindImplementations(context.Background(), workspacesdk.SemanticFindImplementationsRequest{
		Target: workspacesdk.SemanticTarget{Path: path, Line: 2, Column: 6},
		Limit:  20,
	})
	require.Error(t, err)
	var semanticErr *Error
	require.ErrorAs(t, err, &semanticErr)
	require.Equal(t, CodeCapabilityUnsupported, semanticErr.Code)
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestManagerDiagnosticsMixedLanguagesArePartial(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/gopls"); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	root, goPath := writeSemanticFixture(t)
	unsupportedPath := filepath.Join(root, "Example.java")
	require.NoError(t, os.WriteFile(unsupportedPath, []byte("class Example {}\n"), 0o600))

	manager := newLiveGoplsManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	result, err := manager.GetDiagnostics(ctx, workspacesdk.SemanticDiagnosticsRequest{
		Paths: []string{goPath, unsupportedPath}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, result.Files, 2)
	require.Equal(t, coveragePartial, result.Coverage.Status)

	statuses := map[string]string{}
	for _, file := range result.Files {
		statuses[file.Path] = file.Status
	}
	require.Equal(t, statusOK, statuses[goPath])
	require.Equal(t, statusUnsupported, statuses[unsupportedPath])
}

func TestManagerDiagnosticsMixedFailuresReturnPerFileStatuses(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	unsupportedPath := filepath.Join(root, "Example.java")
	require.NoError(t, os.WriteFile(unsupportedPath, []byte("class Example {}\n"), 0o600))
	missingGoPath := filepath.Join(root, "missing.go")

	manager := newLiveGoplsManager(t)
	result, err := manager.GetDiagnostics(context.Background(), workspacesdk.SemanticDiagnosticsRequest{
		Paths: []string{unsupportedPath, missingGoPath},
		Limit: 20,
	})
	require.NoError(t, err)
	require.Empty(t, result.Diagnostics)
	require.Len(t, result.Files, 2)
	require.Equal(t, coveragePartial, result.Coverage.Status)

	statuses := make(map[string]string, len(result.Files))
	for _, file := range result.Files {
		statuses[file.Path] = file.Status
	}
	require.Equal(t, statusUnsupported, statuses[unsupportedPath])
	require.Equal(t, statusError, statuses[missingGoPath])
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestManagerDiagnosticsTracksCurrentFile(t *testing.T) {
	if _, err := os.Stat("/usr/local/bin/gopls"); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	root, _ := writeSemanticFixture(t)
	brokenPath := filepath.Join(root, "broken.go")
	require.NoError(t, os.WriteFile(brokenPath, []byte(`package semantic

var Broken int = "wrong"
`), 0o600))

	manager := newLiveGoplsManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	broken, err := manager.GetDiagnostics(ctx, workspacesdk.SemanticDiagnosticsRequest{
		Paths: []string{brokenPath}, MinimumSeverity: "error", Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, broken.Files, 1)
	require.Equal(t, statusOK, broken.Files[0].Status)
	require.NotEmpty(t, broken.Diagnostics)
	require.Equal(t, "error", broken.Diagnostics[0].Severity)

	unchanged, err := manager.GetDiagnostics(ctx, workspacesdk.SemanticDiagnosticsRequest{
		Paths: []string{brokenPath}, MinimumSeverity: "error", Limit: 20,
	})
	require.NoError(t, err)
	require.Equal(t, broken.Diagnostics, unchanged.Diagnostics, "unchanged pull diagnostics must reuse the matching current snapshot")

	require.NoError(t, os.WriteFile(brokenPath, []byte(`package semantic

var Broken int = 42
`), 0o600))

	clean, err := manager.GetDiagnostics(ctx, workspacesdk.SemanticDiagnosticsRequest{
		Paths: []string{brokenPath}, MinimumSeverity: "error", Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, clean.Files, 1)
	require.Equal(t, statusOK, clean.Files[0].Status)
	require.Empty(t, clean.Diagnostics)
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestManagerReferencesTrackExternalChangeInOtherFile(t *testing.T) {
	if _, err := os.Stat(trustedGoplsPath); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	root, path := writeSemanticFixture(t)
	otherPath := filepath.Join(root, "other.go")
	manager := newLiveGoplsManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	target, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "Target", Match: "exact", Kinds: []string{"function"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, target.Symbols, 1)

	_, err = manager.GetDiagnostics(ctx, workspacesdk.SemanticDiagnosticsRequest{
		Paths: []string{otherPath}, Limit: 20,
	})
	require.NoError(t, err, "open other.go in the semantic overlay before changing it externally")

	before, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: target.Symbols[0].Locator, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, before.References, 2)

	require.NoError(t, os.WriteFile(otherPath, []byte(`package semantic

func OtherCaller() int {
	return 1
}
`), 0o600))

	after, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: target.Symbols[0].Locator, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, after.References, 1, "semantic references must observe an external edit in another project file")
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestManagerShadowedLocalSymbolDoesNotPolluteReferences(t *testing.T) {
	if _, err := os.Stat(trustedGoplsPath); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/shadow\n\ngo 1.26\n"), 0o600))
	path := filepath.Join(root, "shadow.go")
	require.NoError(t, os.WriteFile(path, []byte(`package shadow

func Target(value int) int {
	return value + 1
}

func Caller() int {
	return Target(1)
}

func Shadow() int {
	Target := func(value int) int { return value * 2 }
	return Target(2)
}
`), 0o600))

	manager := newLiveGoplsManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	symbols, err := manager.FindSymbols(ctx, workspacesdk.SemanticFindSymbolsRequest{
		Root: path, Query: "Target", Match: "exact", Kinds: []string{"function"}, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, symbols.Symbols, 1)

	references, err := manager.FindReferences(ctx, workspacesdk.SemanticFindReferencesRequest{
		Target: symbols.Symbols[0].Locator, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, references.References, 1, "local shadowed Target must not count as a reference to the package function")
	require.Equal(t, 8, references.References[0].Range.Start.Line)
}

//nolint:paralleltest // Live gopls integration is intentionally serialized to avoid resource contention.
func TestDiagnosticsLimitAppliesAcrossFiles(t *testing.T) {
	if _, err := os.Stat(trustedGoplsPath); err != nil {
		t.Skipf("gopls is not installed in this test environment: %v", err)
	}

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/diagnosticlimit\n\ngo 1.26\n"), 0o600))
	first := filepath.Join(root, "first.go")
	second := filepath.Join(root, "second.go")
	require.NoError(t, os.WriteFile(first, []byte("package diagnosticlimit\n\nvar First int = \"wrong\"\n"), 0o600))
	require.NoError(t, os.WriteFile(second, []byte("package diagnosticlimit\n\nvar Second int = \"wrong\"\n"), 0o600))

	manager := newLiveGoplsManager(t)
	ctx := testutil.Context(t, testutil.WaitLong)

	result, err := manager.GetDiagnostics(ctx, workspacesdk.SemanticDiagnosticsRequest{
		Paths: []string{first, second}, MinimumSeverity: "error", Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, result.Diagnostics, 1)
	require.GreaterOrEqual(t, result.ObservedCount, 2)
	require.True(t, result.Truncated)
	require.Equal(t, 1, result.ReturnedCount)
}

func TestPublicDiagnosticRelatedInformationToggle(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	relatedPath := filepath.Join(root, "types.go")
	require.NoError(t, os.WriteFile(path, []byte("package sample\nvar Value = 1\n"), 0o600))
	require.NoError(t, os.WriteFile(relatedPath, []byte("package sample\ntype Value int\n"), 0o600))

	session := &semanticSession{
		client: &lspClient{capabilities: lspCapabilities{positionEncoding: "utf-16"}},
	}
	diagnostic := lspDiagnostic{
		Range:    lspRange{Start: lspPosition{Line: 1, Character: 4}, End: lspPosition{Line: 1, Character: 9}},
		Severity: 1,
		Message:  "example error",
		Source:   "compiler",
		Tags:     []int{1, 2},
	}
	diagnostic.RelatedInformation = append(diagnostic.RelatedInformation, struct {
		Location lspLocation `json:"location"`
		Message  string      `json:"message"`
	}{
		Location: lspLocation{
			URI:   pathToFileURI(relatedPath),
			Range: lspRange{Start: lspPosition{Line: 1, Character: 5}, End: lspPosition{Line: 1, Character: 10}},
		},
		Message: "related declaration",
	})

	lines := splitSourceLines("package sample\nvar Value = 1\n")
	withoutRelated, ok := session.publicDiagnostic(path, lines, diagnostic, diagnosticRenderOptions{includeRelated: false, contextLines: 1})
	require.True(t, ok)
	require.Empty(t, withoutRelated.RelatedInformation)
	require.Equal(t, []string{"unnecessary", "deprecated"}, withoutRelated.Tags)
	require.NotNil(t, withoutRelated.Context)

	withRelated, ok := session.publicDiagnostic(path, lines, diagnostic, diagnosticRenderOptions{includeRelated: true, contextLines: 0})
	require.True(t, ok)
	require.Len(t, withRelated.RelatedInformation, 1)
	require.Equal(t, relatedPath, withRelated.RelatedInformation[0].Path)
	require.Equal(t, "related declaration", withRelated.RelatedInformation[0].Message)
}

func TestPublicRangeEndIsExclusive(t *testing.T) {
	t.Parallel()

	got, err := publicRangeFromLSP(
		"/tmp/unicode.go",
		[]string{"🙂x"},
		lspRange{
			Start: lspPosition{Line: 0, Character: 0},
			End:   lspPosition{Line: 0, Character: 2},
		},
		"utf-16",
	)
	require.NoError(t, err)
	require.Equal(t, workspacesdk.SemanticPosition{Line: 1, Column: 1}, got.Start)
	require.Equal(t, workspacesdk.SemanticPosition{Line: 1, Column: 2}, got.End)
}

func TestManagerUnsupportedLanguageIsExplicit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, "Example.java")
	require.NoError(t, os.WriteFile(path, []byte("class Example {}\n"), 0o600))

	manager := newLiveGoplsManager(t)
	_, err := manager.FindReferences(context.Background(), workspacesdk.SemanticFindReferencesRequest{
		Target: workspacesdk.SemanticTarget{Path: path, Line: 1, Column: 1},
		Limit:  20,
	})
	require.Error(t, err)
	var semanticErr *Error
	require.ErrorAs(t, err, &semanticErr)
	require.Equal(t, CodeUnsupportedLanguage, semanticErr.Code)
}
