package agentsemantic

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/modfile"
	"golang.org/x/sync/singleflight"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/agent/agentexec"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	backendStartupTimeout   = 30 * time.Second
	diagnosticsWaitTimeout  = 10 * time.Second
	maxContextLines         = 10
	maxDiagnosticsPaths     = 100
	maxSemanticSourceBytes  = 16 << 20
	trustedGoplsPath        = "/usr/local/bin/gopls"
	languageGo              = "go"
	coverageComplete        = "complete"
	coveragePartial         = "partial"
	coverageUnknown         = "unknown"
	statusOK                = "ok"
	statusUnsupported       = "unsupported_language"
	statusCapabilityMissing = "capability_unsupported"
	statusNotReady          = "not_ready"
	statusError             = "error"
)

const (
	CodeInvalidPath           = "invalid_path"
	CodeInvalidPosition       = "invalid_position"
	CodeUnsupportedLanguage   = "unsupported_language"
	CodeBackendUnavailable    = "semantic_backend_unavailable"
	CodeBackendStartFailed    = "semantic_backend_start_failed"
	CodeBackendNotReady       = "semantic_backend_not_ready"
	CodeCapabilityUnsupported = "capability_unsupported"
	CodeRequestFailed         = "semantic_request_failed"
	CodeResponseTooLarge      = "response_too_large"
)

// Error is a stable semantic operation failure. The Agent HTTP layer serializes
// it as workspacesdk.SemanticErrorResponse.
type Error struct {
	Code    string
	Message string
	Detail  string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return e.Message + ": " + e.Detail
	}
	return e.Message
}

func semanticError(code, message string, err error) error {
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	return &Error{Code: code, Message: message, Detail: detail}
}

type commandEnvUpdater func([]string) ([]string, error)

// Manager owns semantic backends for the lifetime of a workspace Agent.
// Backends are shared across MCP sessions and started lazily.
type Manager struct {
	ctx       context.Context
	cancel    context.CancelFunc
	logger    slog.Logger
	execer    agentexec.Execer
	updateEnv commandEnvUpdater

	mu       sync.Mutex
	sessions map[string]*semanticSession
	startup  singleflight.Group
	closed   bool
}

type semanticSession struct {
	root   string
	client *lspClient

	docsMu sync.Mutex
	docs   map[string]documentState

	pullDiagnosticsMu sync.Mutex
	pullDiagnostics   map[string]pullDiagnosticState
}

type documentState struct {
	hash    [sha256.Size]byte
	version int
	uri     string
}

type syncedDocument struct {
	path    string
	uri     string
	version int
	text    string
	lines   []string
}

type pullDiagnosticState struct {
	version     int
	resultID    string
	diagnostics []lspDiagnostic
}

func NewManager(
	parent context.Context,
	logger slog.Logger,
	execer agentexec.Execer,
	updateEnv commandEnvUpdater,
) *Manager {
	ctx, cancel := context.WithCancel(parent)
	if execer == nil {
		execer = agentexec.DefaultExecer
	}
	return &Manager{
		ctx:       ctx,
		cancel:    cancel,
		logger:    logger,
		execer:    execer,
		updateEnv: updateEnv,
		sessions:  make(map[string]*semanticSession),
	}
}

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	sessions := make([]*semanticSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]*semanticSession)
	m.mu.Unlock()

	var joined error
	for _, session := range sessions {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := session.client.close(ctx)
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			joined = errors.Join(joined, err)
		}
	}
	m.cancel()
	return joined
}

func (m *Manager) ensureOpen() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return semanticError(CodeBackendUnavailable, "Semantic manager is closed.", nil)
	}
	return nil
}

// NotifyPathsChanged synchronizes source files that are already open in an
// active semantic backend. It never starts a backend for an untracked path.
func (m *Manager) NotifyPathsChanged(ctx context.Context, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}

	m.mu.Lock()
	sessions := make([]*semanticSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()
	if len(sessions) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(paths))
	var joined error
	for _, requestedPath := range paths {
		if !filepath.IsAbs(requestedPath) {
			joined = errors.Join(joined, xerrors.Errorf("semantic mutation path must be absolute: %q", requestedPath))
			continue
		}
		path := filepath.Clean(requestedPath)
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = filepath.Clean(resolved)
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		for _, session := range sessions {
			if err := session.refreshTrackedDocument(ctx, path); err != nil {
				joined = errors.Join(joined, xerrors.Errorf("synchronize semantic document %q: %w", path, err))
			}
		}
	}
	return joined
}

func (s *semanticSession) refreshTrackedDocument(ctx context.Context, path string) error {
	s.docsMu.Lock()
	state, tracked := s.docs[path]
	if !tracked {
		s.docsMu.Unlock()
		return nil
	}
	info, statErr := os.Stat(path)
	if statErr != nil || !info.Mode().IsRegular() {
		if err := s.client.notify(ctx, "textDocument/didClose", map[string]any{
			"textDocument": map[string]any{"uri": state.uri},
		}); err != nil {
			s.docsMu.Unlock()
			return err
		}
		delete(s.docs, path)
		s.docsMu.Unlock()

		s.pullDiagnosticsMu.Lock()
		delete(s.pullDiagnostics, state.uri)
		s.pullDiagnosticsMu.Unlock()

		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		return nil
	}
	s.docsMu.Unlock()

	_, err := s.syncDocument(ctx, path, syncIfChanged)
	return err
}

func (m *Manager) getSession(ctx context.Context, root string) (*semanticSession, error) {
	if err := m.ensureOpen(); err != nil {
		return nil, err
	}
	root, err := canonicalExistingPath(root)
	if err != nil {
		return nil, semanticError(CodeInvalidPath, "Semantic project root is invalid.", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = xerrors.Errorf("%q is not a directory", root)
		}
		return nil, semanticError(CodeInvalidPath, "Semantic project root is invalid.", err)
	}

	key := languageGo + ":" + root
	m.mu.Lock()
	if session := m.sessions[key]; session != nil {
		if session.client.alive() {
			m.mu.Unlock()
			m.logger.Debug(ctx, "semantic backend reused", slog.F("language", languageGo), slog.F("root", root))
			return session, nil
		}
		delete(m.sessions, key)
	}
	m.mu.Unlock()

	resultCh := m.startup.DoChan(key, func() (any, error) {
		started := time.Now()
		m.mu.Lock()
		if session := m.sessions[key]; session != nil {
			if session.client.alive() {
				m.mu.Unlock()
				m.logger.Debug(m.ctx, "semantic backend reused", slog.F("language", languageGo), slog.F("root", root))
				return session, nil
			}
			delete(m.sessions, key)
		}
		closed := m.closed
		m.mu.Unlock()
		if closed {
			return nil, semanticError(CodeBackendUnavailable, "Semantic manager is closed.", nil)
		}

		if info, err := os.Stat(trustedGoplsPath); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			if err == nil {
				err = xerrors.Errorf("%s is not a regular executable file", trustedGoplsPath)
			}
			return nil, semanticError(
				CodeBackendUnavailable,
				"Pinned Go semantic backend gopls is unavailable in this workspace.",
				err,
			)
		}

		startCtx, startCancel := context.WithTimeout(m.ctx, backendStartupTimeout)
		defer startCancel()

		command := func(processCtx context.Context) *exec.Cmd {
			cmd := m.execer.CommandContext(processCtx, trustedGoplsPath)
			cmd.Dir = root
			env := os.Environ()
			if m.updateEnv != nil {
				updated, envErr := m.updateEnv(env)
				if envErr != nil {
					m.logger.Warn(processCtx, "failed to enrich gopls environment; using inherited environment", slog.Error(envErr))
				} else {
					env = updated
				}
			}
			cmd.Env = env
			return cmd
		}
		client, err := newLSPClient(m.ctx, root, command)
		if err != nil {
			return nil, semanticError(CodeBackendStartFailed, "Failed to start Go semantic backend.", err)
		}
		if err := client.initialize(startCtx); err != nil {
			closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
			_ = client.close(closeCtx)
			closeCancel()
			detail := err
			if stderr := client.stderrString(); stderr != "" {
				detail = xerrors.Errorf("%w; gopls stderr: %s", err, stderr)
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, semanticError(CodeBackendNotReady, "Go semantic backend did not initialize before the startup deadline.", detail)
			}
			return nil, semanticError(CodeBackendStartFailed, "Failed to initialize Go semantic backend.", detail)
		}

		session := &semanticSession{
			root:            root,
			client:          client,
			docs:            make(map[string]documentState),
			pullDiagnostics: make(map[string]pullDiagnosticState),
		}
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			closeCtx, closeCancel := context.WithTimeout(context.Background(), time.Second)
			_ = client.close(closeCtx)
			closeCancel()
			return nil, semanticError(CodeBackendUnavailable, "Semantic manager is closed.", nil)
		}
		m.sessions[key] = session
		m.mu.Unlock()
		m.logger.Debug(
			m.ctx,
			"semantic backend ready",
			slog.F("language", languageGo),
			slog.F("root", root),
			slog.F("initialization_ms", time.Since(started).Milliseconds()),
		)
		return session, nil
	})

	select {
	case <-ctx.Done():
		return nil, semanticError(CodeBackendNotReady, "Semantic backend startup is still in progress.", ctx.Err())
	case <-m.ctx.Done():
		return nil, semanticError(CodeBackendUnavailable, "Semantic manager is shutting down.", m.ctx.Err())
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		session, ok := result.Val.(*semanticSession)
		if !ok || session == nil {
			return nil, semanticError(CodeBackendStartFailed, "Semantic backend startup returned an invalid session.", nil)
		}
		return session, nil
	}
}

func canonicalExistingPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", xerrors.New("path is required")
	}
	if !filepath.IsAbs(path) {
		return "", xerrors.Errorf("path must be absolute: %q", path)
	}
	clean := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func readSemanticSource(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, xerrors.Errorf("%q is not a regular file", path)
	}
	if info.Size() > maxSemanticSourceBytes {
		return nil, xerrors.Errorf("semantic source file %q is %d bytes, exceeding internal safety limit %d", path, info.Size(), maxSemanticSourceBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSemanticSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSemanticSourceBytes {
		return nil, xerrors.Errorf("semantic source file %q exceeds internal safety limit %d", path, maxSemanticSourceBytes)
	}
	return data, nil
}

func validateContextLines(lines int) error {
	if lines < 0 || lines > maxContextLines {
		return semanticError(CodeInvalidPath, fmt.Sprintf("context_lines must be between 0 and %d.", maxContextLines), nil)
	}
	return nil
}

func validateLimit(limit int) error {
	if limit < 0 {
		return semanticError(CodeInvalidPath, "limit cannot be negative.", nil)
	}
	return nil
}

func validateGoFile(path string) (string, error) {
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		return "", semanticError(CodeInvalidPath, "Semantic file path is invalid.", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", semanticError(CodeInvalidPath, "Semantic file path is invalid.", err)
	}
	if !info.Mode().IsRegular() {
		return "", semanticError(CodeInvalidPath, "Semantic file path must be a regular file.", xerrors.Errorf("%q is not a regular file", canonical))
	}
	if strings.ToLower(filepath.Ext(canonical)) != ".go" {
		return "", semanticError(
			CodeUnsupportedLanguage,
			fmt.Sprintf("No semantic backend is available for %s in this workspace. The current semantic implementation supports Go only.", detectedLanguage(canonical)),
			nil,
		)
	}
	return canonical, nil
}

func detectedLanguage(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "Go"
	case ".py", ".pyi":
		return "Python"
	case ".ts", ".tsx":
		return "TypeScript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "JavaScript"
	case ".rs":
		return "Rust"
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp":
		return "C/C++"
	case ".java":
		return "Java"
	default:
		ext := filepath.Ext(path)
		if ext != "" {
			return strings.TrimPrefix(ext, ".")
		}
		return "this file type"
	}
}

func findUpward(startDir, name string) string {
	dir := filepath.Clean(startDir)
	for {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

type goWorkSearchMode int

const (
	goWorkContainingTarget goWorkSearchMode = iota
	goWorkContainingOrDescendant
)

func findApplicableGoWork(startDir string, mode goWorkSearchMode) (string, error) {
	targetDir, err := canonicalExistingPath(startDir)
	if err != nil {
		return "", err
	}
	dir := targetDir
	for {
		candidate := filepath.Join(dir, "go.work")
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			data, readErr := os.ReadFile(candidate)
			if readErr != nil {
				return "", readErr
			}
			workFile, parseErr := modfile.ParseWork(candidate, data, nil)
			if parseErr != nil {
				return "", xerrors.Errorf("parse %s: %w", candidate, parseErr)
			}
			for _, use := range workFile.Use {
				usePath := use.Path
				if !filepath.IsAbs(usePath) {
					usePath = filepath.Join(dir, usePath)
				}
				usePath = filepath.Clean(usePath)
				if resolved, resolveErr := filepath.EvalSymlinks(usePath); resolveErr == nil {
					usePath = resolved
				}
				if pathWithin(usePath, targetDir) || (mode == goWorkContainingOrDescendant && pathWithin(targetDir, usePath)) {
					return candidate, nil
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

func goProjectRootForFile(path string) (string, error) {
	path, err := canonicalExistingPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	dir := path
	if !info.IsDir() {
		dir = filepath.Dir(path)
	}
	goWork, err := findApplicableGoWork(dir, goWorkContainingTarget)
	if err != nil {
		return "", err
	}
	if goWork != "" {
		return filepath.Dir(goWork), nil
	}
	if goMod := findUpward(dir, "go.mod"); goMod != "" {
		return filepath.Dir(goMod), nil
	}
	return dir, nil
}

func goProjectRootsForDirectory(root string) ([]string, workspacesdk.SemanticCoverage, error) {
	root, err := canonicalExistingPath(root)
	if err != nil {
		return nil, workspacesdk.SemanticCoverage{}, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, workspacesdk.SemanticCoverage{}, err
	}
	if !info.IsDir() {
		projectRoot, rootErr := goProjectRootForFile(root)
		return []string{projectRoot}, workspacesdk.SemanticCoverage{Status: coverageComplete}, rootErr
	}

	moduleRoots, err := goModuleRootsUnder(root)
	if err != nil {
		return nil, workspacesdk.SemanticCoverage{}, err
	}
	roots := make(map[string]struct{})

	goWork, err := findApplicableGoWork(root, goWorkContainingOrDescendant)
	if err != nil {
		return nil, workspacesdk.SemanticCoverage{}, err
	}
	if goWork != "" {
		workRoot := filepath.Dir(goWork)
		roots[workRoot] = struct{}{}
		useRoots, useErr := goWorkUseRoots(goWork)
		if useErr != nil {
			return nil, workspacesdk.SemanticCoverage{}, useErr
		}
		for _, moduleRoot := range moduleRoots {
			covered := false
			for _, useRoot := range useRoots {
				if pathWithin(useRoot, moduleRoot) {
					covered = true
					break
				}
			}
			if !covered {
				// The requested directory contains a Go module that is not part
				// of the enclosing go.work. Query it separately rather than
				// silently dropping a known semantic root.
				roots[moduleRoot] = struct{}{}
			}
		}
	} else {
		if goMod := findUpward(root, "go.mod"); goMod != "" {
			roots[filepath.Dir(goMod)] = struct{}{}
		}
		for _, moduleRoot := range moduleRoots {
			roots[moduleRoot] = struct{}{}
		}
	}

	if len(roots) == 0 {
		roots[root] = struct{}{}
	}
	result := make([]string, 0, len(roots))
	for projectRoot := range roots {
		result = append(result, projectRoot)
	}
	sort.Strings(result)
	return result, workspacesdk.SemanticCoverage{
		Status: coverageUnknown,
		Reason: "Workspace symbol completeness depends on the semantic backend index state.",
	}, nil
}

func goModuleRootsUnder(root string) ([]string, error) {
	roots := make(map[string]struct{})
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "node_modules":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if entry.Name() == "go.mod" {
			roots[filepath.Dir(path)] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(roots))
	for moduleRoot := range roots {
		result = append(result, moduleRoot)
	}
	sort.Strings(result)
	return result, nil
}

func goWorkUseRoots(goWork string) ([]string, error) {
	data, err := os.ReadFile(goWork)
	if err != nil {
		return nil, err
	}
	workFile, err := modfile.ParseWork(goWork, data, nil)
	if err != nil {
		return nil, xerrors.Errorf("parse %s: %w", goWork, err)
	}
	workRoot := filepath.Dir(goWork)
	roots := make([]string, 0, len(workFile.Use))
	for _, use := range workFile.Use {
		useRoot := use.Path
		if !filepath.IsAbs(useRoot) {
			useRoot = filepath.Join(workRoot, useRoot)
		}
		useRoot = filepath.Clean(useRoot)
		if resolved, resolveErr := filepath.EvalSymlinks(useRoot); resolveErr == nil {
			useRoot = resolved
		}
		roots = append(roots, useRoot)
	}
	return roots, nil
}

type documentSyncMode int

const (
	syncIfChanged documentSyncMode = iota
	syncForceChange
)

func (s *semanticSession) syncDocument(ctx context.Context, path string, mode documentSyncMode) (syncedDocument, error) {
	s.docsMu.Lock()
	defer s.docsMu.Unlock()

	data, err := readSemanticSource(path)
	if err != nil {
		return syncedDocument{}, err
	}
	text := string(data)
	hash := sha256.Sum256(data)
	uri := pathToFileURI(path)
	state, exists := s.docs[path]
	if !exists {
		state = documentState{hash: hash, version: 1, uri: uri}
		if err := s.client.notify(ctx, "textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": languageGo,
				"version":    state.version,
				"text":       text,
			},
		}); err != nil {
			return syncedDocument{}, err
		}
		s.docs[path] = state
	} else if state.hash != hash || mode == syncForceChange {
		state.version++
		state.hash = hash
		if err := s.client.notify(ctx, "textDocument/didChange", map[string]any{
			"textDocument": map[string]any{
				"uri":     uri,
				"version": state.version,
			},
			"contentChanges": []map[string]any{{"text": text}},
		}); err != nil {
			return syncedDocument{}, err
		}
		s.docs[path] = state
	}
	return syncedDocument{
		path:    path,
		uri:     uri,
		version: state.version,
		text:    text,
		lines:   splitSourceLines(text),
	}, nil
}

func splitSourceLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

func lspPositionForPublic(doc syncedDocument, pos workspacesdk.SemanticPosition, encoding string) (lspPosition, error) {
	if pos.Line < 1 || pos.Line > len(doc.lines) {
		return lspPosition{}, semanticError(CodeInvalidPosition, "Semantic source line is outside the file.", xerrors.Errorf("line %d, file has %d lines", pos.Line, len(doc.lines)))
	}
	column, err := publicColumnToLSP(doc.lines[pos.Line-1], pos.Column, encoding)
	if err != nil {
		return lspPosition{}, semanticError(CodeInvalidPosition, "Semantic source column is outside the line.", err)
	}
	return lspPosition{Line: pos.Line - 1, Character: column}, nil
}

func publicRangeFromLSP(path string, lines []string, rng lspRange, encoding string) (workspacesdk.SemanticRange, error) {
	convert := func(pos lspPosition) (workspacesdk.SemanticPosition, error) {
		line := pos.Line + 1
		if line < 1 || line > len(lines) {
			return workspacesdk.SemanticPosition{}, xerrors.Errorf("LSP line %d is outside %s", pos.Line, path)
		}
		column, err := lspColumnToPublic(lines[pos.Line], pos.Character, encoding)
		if err != nil {
			return workspacesdk.SemanticPosition{}, err
		}
		return workspacesdk.SemanticPosition{Line: line, Column: column}, nil
	}
	start, err := convert(rng.Start)
	if err != nil {
		return workspacesdk.SemanticRange{}, err
	}
	end, err := convert(rng.End)
	if err != nil {
		return workspacesdk.SemanticRange{}, err
	}
	return workspacesdk.SemanticRange{Start: start, End: end}, nil
}

func sourceContext(lines []string, rng workspacesdk.SemanticRange, contextLines int) *workspacesdk.SemanticContext {
	if contextLines <= 0 || len(lines) == 0 {
		return nil
	}
	start := rng.Start.Line - contextLines
	if start < 1 {
		start = 1
	}
	end := rng.End.Line + contextLines
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return nil
	}
	return &workspacesdk.SemanticContext{
		StartLine: start,
		EndLine:   end,
		Text:      strings.Join(lines[start-1:end], "\n"),
	}
}

func pathWithin(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func applyLimit[T any](values []T, limit int) ([]T, bool) {
	if limit <= 0 || len(values) <= limit {
		return values, false
	}
	return values[:limit], true
}

func symbolKindName(kind int) string {
	switch kind {
	case 1:
		return "file"
	case 2:
		return "module"
	case 3:
		return "namespace"
	case 4:
		return "package"
	case 5:
		return "class"
	case 6:
		return "method"
	case 7:
		return "property"
	case 8:
		return "field"
	case 9:
		return "constructor"
	case 10:
		return "enum"
	case 11:
		return "interface"
	case 12:
		return "function"
	case 13:
		return "variable"
	case 14:
		return "constant"
	case 15:
		return "string"
	case 16:
		return "number"
	case 17:
		return "boolean"
	case 18:
		return "array"
	case 19:
		return "object"
	case 20:
		return "key"
	case 21:
		return "null"
	case 22:
		return "enum_member"
	case 23:
		return "struct"
	case 24:
		return "event"
	case 25:
		return "operator"
	case 26:
		return "type_parameter"
	default:
		return "unknown"
	}
}

func matchSymbolName(name, query, match string) bool {
	switch match {
	case "", "exact":
		return name == query
	case "prefix":
		return strings.HasPrefix(name, query)
	case "substring":
		return strings.Contains(name, query)
	default:
		return false
	}
}

func symbolMatchRank(name, query string) int {
	switch {
	case name == query:
		return 0
	case strings.HasPrefix(name, query):
		return 1
	default:
		return 2
	}
}

func allowedSymbolKind(kind string, kinds []string) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, requested := range kinds {
		if requested == kind {
			return true
		}
	}
	return false
}

// normalizeGoSymbolIdentity hides gopls' receiver-qualified method display
// names from the language-neutral public API. For example, gopls may expose
// "(*Server).ServeHTTP"; the public semantic symbol is name="ServeHTTP" with
// container="Server".
func normalizeGoSymbolIdentity(name, kind, container string) (symbolName, symbolContainer string) {
	if kind != "method" {
		return name, container
	}
	dot := strings.LastIndex(name, ".")
	if dot <= 0 || dot == len(name)-1 {
		return name, container
	}
	receiver := strings.TrimSpace(name[:dot])
	method := name[dot+1:]
	receiver = strings.TrimPrefix(receiver, "(")
	receiver = strings.TrimSuffix(receiver, ")")
	receiver = strings.TrimPrefix(receiver, "*")
	receiver = strings.TrimSpace(receiver)
	if receiver != "" {
		if packageDot := strings.LastIndex(receiver, "."); packageDot >= 0 && packageDot < len(receiver)-1 {
			receiver = receiver[packageDot+1:]
		}
		container = receiver
	}
	return method, container
}

type lspDocumentSymbol struct {
	Name           string              `json:"name"`
	Detail         string              `json:"detail,omitempty"`
	Kind           int                 `json:"kind"`
	Range          lspRange            `json:"range"`
	SelectionRange lspRange            `json:"selectionRange"`
	Children       []lspDocumentSymbol `json:"children,omitempty"`
}

type lspSymbolInformation struct {
	Name          string      `json:"name"`
	Kind          int         `json:"kind"`
	Location      lspLocation `json:"location"`
	ContainerName string      `json:"containerName,omitempty"`
}

func (m *Manager) FindSymbols(ctx context.Context, req workspacesdk.SemanticFindSymbolsRequest) (workspacesdk.SemanticFindSymbolsResponse, error) {
	if err := validateLimit(req.Limit); err != nil {
		return workspacesdk.SemanticFindSymbolsResponse{}, err
	}
	if err := validateContextLines(req.ContextLines); err != nil {
		return workspacesdk.SemanticFindSymbolsResponse{}, err
	}
	if req.Query == "" {
		return workspacesdk.SemanticFindSymbolsResponse{}, semanticError(CodeInvalidPath, "query is required.", nil)
	}
	switch req.Match {
	case "", "exact", "prefix", "substring":
	default:
		return workspacesdk.SemanticFindSymbolsResponse{}, semanticError(CodeInvalidPath, "match must be exact, prefix, or substring.", nil)
	}

	root, err := canonicalExistingPath(req.Root)
	if err != nil {
		return workspacesdk.SemanticFindSymbolsResponse{}, semanticError(CodeInvalidPath, "Semantic symbol root is invalid.", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return workspacesdk.SemanticFindSymbolsResponse{}, semanticError(CodeInvalidPath, "Semantic symbol root is invalid.", err)
	}

	symbols := make([]workspacesdk.SemanticSymbol, 0)
	coverage := workspacesdk.SemanticCoverage{Status: coverageComplete}
	switch {
	case info.Mode().IsRegular():
		if strings.ToLower(filepath.Ext(root)) != ".go" {
			return workspacesdk.SemanticFindSymbolsResponse{}, semanticError(
				CodeUnsupportedLanguage,
				fmt.Sprintf("No semantic backend is available for %s in this workspace. The current semantic implementation supports Go only.", detectedLanguage(root)),
				nil,
			)
		}
		projectRoot, rootErr := goProjectRootForFile(root)
		if rootErr != nil {
			return workspacesdk.SemanticFindSymbolsResponse{}, semanticError(CodeInvalidPath, "Unable to determine Go semantic project root.", rootErr)
		}
		session, sessionErr := m.getSession(ctx, projectRoot)
		if sessionErr != nil {
			return workspacesdk.SemanticFindSymbolsResponse{}, sessionErr
		}
		fileSymbols, fileErr := session.findDocumentSymbols(ctx, root, req)
		if fileErr != nil {
			return workspacesdk.SemanticFindSymbolsResponse{}, fileErr
		}
		symbols = append(symbols, fileSymbols...)
	case info.IsDir():
		roots, directoryCoverage, rootErr := goProjectRootsForDirectory(root)
		if rootErr != nil {
			return workspacesdk.SemanticFindSymbolsResponse{}, semanticError(CodeInvalidPath, "Unable to determine Go semantic project roots.", rootErr)
		}
		coverage = directoryCoverage
		successfulRoots := 0
		var firstRootErr error
		rootFailures := make([]string, 0)
		for _, projectRoot := range roots {
			session, sessionErr := m.getSession(ctx, projectRoot)
			if sessionErr != nil {
				if firstRootErr == nil {
					firstRootErr = sessionErr
				}
				rootFailures = append(rootFailures, sessionErr.Error())
				continue
			}
			projectSymbols, symbolErr := session.findWorkspaceSymbols(ctx, root, req)
			if symbolErr != nil {
				if firstRootErr == nil {
					firstRootErr = symbolErr
				}
				rootFailures = append(rootFailures, symbolErr.Error())
				continue
			}
			successfulRoots++
			symbols = append(symbols, projectSymbols...)
		}
		if successfulRoots == 0 && firstRootErr != nil {
			return workspacesdk.SemanticFindSymbolsResponse{}, firstRootErr
		}
		if len(rootFailures) > 0 {
			coverage = workspacesdk.SemanticCoverage{
				Status: coveragePartial,
				Reason: strings.Join(rootFailures, "; "),
			}
		}
	default:
		return workspacesdk.SemanticFindSymbolsResponse{}, semanticError(CodeInvalidPath, "Semantic symbol root must be a regular file or directory.", nil)
	}

	symbols = dedupeSymbols(symbols)
	sort.Slice(symbols, func(i, j int) bool {
		rankI := symbolMatchRank(symbols[i].Name, req.Query)
		rankJ := symbolMatchRank(symbols[j].Name, req.Query)
		if rankI != rankJ {
			return rankI < rankJ
		}
		if symbols[i].Name != symbols[j].Name {
			return symbols[i].Name < symbols[j].Name
		}
		if symbols[i].Path != symbols[j].Path {
			return symbols[i].Path < symbols[j].Path
		}
		if symbols[i].SelectionRange.Start.Line != symbols[j].SelectionRange.Start.Line {
			return symbols[i].SelectionRange.Start.Line < symbols[j].SelectionRange.Start.Line
		}
		return symbols[i].SelectionRange.Start.Column < symbols[j].SelectionRange.Start.Column
	})
	observed := len(symbols)
	symbols, truncated := applyLimit(symbols, req.Limit)
	return workspacesdk.SemanticFindSymbolsResponse{
		Symbols:       symbols,
		ReturnedCount: len(symbols),
		ObservedCount: observed,
		Truncated:     truncated,
		Coverage:      coverage,
	}, nil
}

func (s *semanticSession) findDocumentSymbols(ctx context.Context, path string, req workspacesdk.SemanticFindSymbolsRequest) ([]workspacesdk.SemanticSymbol, error) {
	if !s.client.capabilities.documentSymbols {
		return nil, semanticError(CodeCapabilityUnsupported, "The Go semantic backend does not support document symbol lookup.", nil)
	}
	doc, err := s.syncDocument(ctx, path, syncIfChanged)
	if err != nil {
		return nil, semanticError(CodeRequestFailed, "Failed to synchronize Go document before symbol lookup.", err)
	}
	raw, err := s.client.request(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": doc.uri},
	})
	if err != nil {
		return nil, semanticRequestError("Find symbol request failed.", err)
	}
	if string(raw) == "null" || len(raw) == 0 {
		return []workspacesdk.SemanticSymbol{}, nil
	}

	documentSymbols, flatSymbols, hierarchical, err := decodeDocumentSymbolResponse(raw)
	if err != nil {
		return nil, semanticError(CodeRequestFailed, "Failed to decode document symbols.", err)
	}
	if hierarchical {
		var out []workspacesdk.SemanticSymbol
		var visit func([]lspDocumentSymbol, string) error
		visit = func(items []lspDocumentSymbol, container string) error {
			for _, item := range items {
				kind := symbolKindName(item.Kind)
				name, symbolContainer := normalizeGoSymbolIdentity(item.Name, kind, container)
				if matchSymbolName(name, req.Query, req.Match) && allowedSymbolKind(kind, req.Kinds) {
					fullRange, rangeErr := publicRangeFromLSP(path, doc.lines, item.Range, s.client.capabilities.positionEncoding)
					if rangeErr != nil {
						return rangeErr
					}
					selection, selectionErr := publicRangeFromLSP(path, doc.lines, item.SelectionRange, s.client.capabilities.positionEncoding)
					if selectionErr != nil {
						return selectionErr
					}
					out = append(out, workspacesdk.SemanticSymbol{
						Name:           name,
						Kind:           kind,
						Language:       languageGo,
						Path:           path,
						Container:      symbolContainer,
						Detail:         item.Detail,
						Range:          &fullRange,
						SelectionRange: selection,
						Locator: workspacesdk.SemanticTarget{
							Path: path, Line: selection.Start.Line, Column: selection.Start.Column,
						},
						Context: sourceContext(doc.lines, selection, req.ContextLines),
					})
				}
				childContainer := item.Name
				if kind == "method" {
					childContainer = symbolContainer
				}
				if err := visit(item.Children, childContainer); err != nil {
					return err
				}
			}
			return nil
		}
		if err := visit(documentSymbols, ""); err != nil {
			return nil, semanticError(CodeRequestFailed, "Failed to normalize document symbols.", err)
		}
		return out, nil
	}

	return s.symbolInformationToPublic(flatSymbols, filepath.Dir(path), req)
}

func decodeDocumentSymbolResponse(raw json.RawMessage) ([]lspDocumentSymbol, []lspSymbolInformation, bool, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, false, err
	}
	if len(items) == 0 {
		return []lspDocumentSymbol{}, nil, true, nil
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(items[0], &probe); err != nil {
		return nil, nil, false, err
	}
	if _, ok := probe["selectionRange"]; ok {
		var hierarchical []lspDocumentSymbol
		if err := json.Unmarshal(raw, &hierarchical); err != nil {
			return nil, nil, false, err
		}
		return hierarchical, nil, true, nil
	}
	if _, ok := probe["location"]; ok {
		var flat []lspSymbolInformation
		if err := json.Unmarshal(raw, &flat); err != nil {
			return nil, nil, false, err
		}
		return nil, flat, false, nil
	}
	return nil, nil, false, xerrors.New("documentSymbol result is neither DocumentSymbol[] nor SymbolInformation[]")
}

func (s *semanticSession) findWorkspaceSymbols(ctx context.Context, requestedRoot string, req workspacesdk.SemanticFindSymbolsRequest) ([]workspacesdk.SemanticSymbol, error) {
	if !s.client.capabilities.workspaceSymbols {
		return nil, semanticError(CodeCapabilityUnsupported, "The Go semantic backend does not support workspace symbol lookup.", nil)
	}
	var items []lspSymbolInformation
	if err := s.client.requestInto(ctx, "workspace/symbol", map[string]any{"query": req.Query}, &items); err != nil {
		return nil, semanticRequestError("Find symbol request failed.", err)
	}
	return s.symbolInformationToPublic(items, requestedRoot, req)
}

func (s *semanticSession) symbolInformationToPublic(items []lspSymbolInformation, requestedRoot string, req workspacesdk.SemanticFindSymbolsRequest) ([]workspacesdk.SemanticSymbol, error) {
	out := make([]workspacesdk.SemanticSymbol, 0, len(items))
	for _, item := range items {
		path, err := fileURIToPath(item.Location.URI)
		if err != nil {
			continue
		}
		path, err = canonicalExistingPath(path)
		if err != nil || !pathWithin(requestedRoot, path) {
			continue
		}
		kind := symbolKindName(item.Kind)
		name, container := normalizeGoSymbolIdentity(item.Name, kind, item.ContainerName)
		if !matchSymbolName(name, req.Query, req.Match) || !allowedSymbolKind(kind, req.Kinds) {
			continue
		}
		data, err := readSemanticSource(path)
		if err != nil {
			continue
		}
		lines := splitSourceLines(string(data))
		publicRange, err := publicRangeFromLSP(path, lines, item.Location.Range, s.client.capabilities.positionEncoding)
		if err != nil {
			continue
		}
		out = append(out, workspacesdk.SemanticSymbol{
			Name:           name,
			Kind:           kind,
			Language:       languageGo,
			Path:           path,
			Container:      container,
			SelectionRange: publicRange,
			Locator: workspacesdk.SemanticTarget{
				Path: path, Line: publicRange.Start.Line, Column: publicRange.Start.Column,
			},
			Context: sourceContext(lines, publicRange, req.ContextLines),
		})
	}
	return out, nil
}

func dedupeSymbols(values []workspacesdk.SemanticSymbol) []workspacesdk.SemanticSymbol {
	seen := make(map[string]struct{}, len(values))
	out := make([]workspacesdk.SemanticSymbol, 0, len(values))
	for _, value := range values {
		key := fmt.Sprintf("%s:%d:%d:%s:%s", value.Path, value.SelectionRange.Start.Line, value.SelectionRange.Start.Column, value.Kind, value.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func semanticRequestError(message string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return semanticError(CodeBackendNotReady, message, err)
	}
	return semanticError(CodeRequestFailed, message, err)
}

func (m *Manager) FindReferences(ctx context.Context, req workspacesdk.SemanticFindReferencesRequest) (workspacesdk.SemanticFindReferencesResponse, error) {
	if err := validateLimit(req.Limit); err != nil {
		return workspacesdk.SemanticFindReferencesResponse{}, err
	}
	if err := validateContextLines(req.ContextLines); err != nil {
		return workspacesdk.SemanticFindReferencesResponse{}, err
	}
	path, err := validateGoFile(req.Target.Path)
	if err != nil {
		return workspacesdk.SemanticFindReferencesResponse{}, err
	}
	scopePath, err := optionalScopePath(req.ScopePath)
	if err != nil {
		return workspacesdk.SemanticFindReferencesResponse{}, err
	}
	projectRoot, err := goProjectRootForFile(path)
	if err != nil {
		return workspacesdk.SemanticFindReferencesResponse{}, semanticError(CodeInvalidPath, "Unable to determine Go semantic project root.", err)
	}
	session, err := m.getSession(ctx, projectRoot)
	if err != nil {
		return workspacesdk.SemanticFindReferencesResponse{}, err
	}
	if !session.client.capabilities.references {
		return workspacesdk.SemanticFindReferencesResponse{}, semanticError(CodeCapabilityUnsupported, "The Go semantic backend does not support reference lookup.", nil)
	}
	doc, err := session.syncDocument(ctx, path, syncIfChanged)
	if err != nil {
		return workspacesdk.SemanticFindReferencesResponse{}, semanticError(CodeRequestFailed, "Failed to synchronize Go document before reference lookup.", err)
	}
	position, err := lspPositionForPublic(doc, workspacesdk.SemanticPosition{Line: req.Target.Line, Column: req.Target.Column}, session.client.capabilities.positionEncoding)
	if err != nil {
		return workspacesdk.SemanticFindReferencesResponse{}, err
	}
	var locations []lspLocation
	if err := session.client.requestInto(ctx, "textDocument/references", map[string]any{
		"textDocument": map[string]any{"uri": doc.uri},
		"position":     position,
		"context":      map[string]any{"includeDeclaration": req.IncludeDeclaration},
	}, &locations); err != nil {
		return workspacesdk.SemanticFindReferencesResponse{}, semanticRequestError("Find references request failed.", err)
	}
	references := make([]workspacesdk.SemanticReference, 0, len(locations))
	for _, location := range locations {
		reference, ok := session.referenceFromLocation(location, req.ContextLines, scopePath)
		if ok {
			references = append(references, reference)
		}
	}
	references = dedupeReferences(references)
	sort.Slice(references, func(i, j int) bool {
		if references[i].Path != references[j].Path {
			return references[i].Path < references[j].Path
		}
		if references[i].Range.Start.Line != references[j].Range.Start.Line {
			return references[i].Range.Start.Line < references[j].Range.Start.Line
		}
		if references[i].Range.Start.Column != references[j].Range.Start.Column {
			return references[i].Range.Start.Column < references[j].Range.Start.Column
		}
		if references[i].Range.End.Line != references[j].Range.End.Line {
			return references[i].Range.End.Line < references[j].Range.End.Line
		}
		return references[i].Range.End.Column < references[j].Range.End.Column
	})
	observed := len(references)
	references, truncated := applyLimit(references, req.Limit)
	return workspacesdk.SemanticFindReferencesResponse{
		Target:        workspacesdk.SemanticTarget{Path: path, Line: req.Target.Line, Column: req.Target.Column},
		References:    references,
		ReturnedCount: len(references),
		ObservedCount: observed,
		Truncated:     truncated,
		Coverage: workspacesdk.SemanticCoverage{
			Status: coverageUnknown,
			Reason: "Reference completeness depends on the semantic backend index state.",
		},
	}, nil
}

func optionalScopePath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	canonical, err := canonicalExistingPath(path)
	if err != nil {
		return "", semanticError(CodeInvalidPath, "Semantic scope_path is invalid.", err)
	}
	return canonical, nil
}

func (s *semanticSession) referenceFromLocation(location lspLocation, contextLines int, scopePath string) (workspacesdk.SemanticReference, bool) {
	path, err := fileURIToPath(location.URI)
	if err != nil {
		return workspacesdk.SemanticReference{}, false
	}
	path, err = canonicalExistingPath(path)
	if err != nil {
		return workspacesdk.SemanticReference{}, false
	}
	if scopePath != "" && !pathWithin(scopePath, path) {
		return workspacesdk.SemanticReference{}, false
	}
	data, err := readSemanticSource(path)
	if err != nil {
		return workspacesdk.SemanticReference{}, false
	}
	lines := splitSourceLines(string(data))
	publicRange, err := publicRangeFromLSP(path, lines, location.Range, s.client.capabilities.positionEncoding)
	if err != nil {
		return workspacesdk.SemanticReference{}, false
	}
	return workspacesdk.SemanticReference{
		Path:     path,
		Language: languageGo,
		Range:    publicRange,
		Locator: workspacesdk.SemanticTarget{
			Path: path, Line: publicRange.Start.Line, Column: publicRange.Start.Column,
		},
		Context: sourceContext(lines, publicRange, contextLines),
	}, true
}

func dedupeReferences(values []workspacesdk.SemanticReference) []workspacesdk.SemanticReference {
	seen := make(map[string]struct{}, len(values))
	out := make([]workspacesdk.SemanticReference, 0, len(values))
	for _, value := range values {
		key := fmt.Sprintf("%s:%d:%d:%d:%d", value.Path, value.Range.Start.Line, value.Range.Start.Column, value.Range.End.Line, value.Range.End.Column)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (m *Manager) FindImplementations(ctx context.Context, req workspacesdk.SemanticFindImplementationsRequest) (workspacesdk.SemanticFindImplementationsResponse, error) {
	if err := validateLimit(req.Limit); err != nil {
		return workspacesdk.SemanticFindImplementationsResponse{}, err
	}
	if err := validateContextLines(req.ContextLines); err != nil {
		return workspacesdk.SemanticFindImplementationsResponse{}, err
	}
	path, err := validateGoFile(req.Target.Path)
	if err != nil {
		return workspacesdk.SemanticFindImplementationsResponse{}, err
	}
	scopePath, err := optionalScopePath(req.ScopePath)
	if err != nil {
		return workspacesdk.SemanticFindImplementationsResponse{}, err
	}
	projectRoot, err := goProjectRootForFile(path)
	if err != nil {
		return workspacesdk.SemanticFindImplementationsResponse{}, semanticError(CodeInvalidPath, "Unable to determine Go semantic project root.", err)
	}
	session, err := m.getSession(ctx, projectRoot)
	if err != nil {
		return workspacesdk.SemanticFindImplementationsResponse{}, err
	}
	if !session.client.capabilities.implementations {
		return workspacesdk.SemanticFindImplementationsResponse{}, semanticError(CodeCapabilityUnsupported, "The Go semantic backend does not support implementation lookup.", nil)
	}
	doc, err := session.syncDocument(ctx, path, syncIfChanged)
	if err != nil {
		return workspacesdk.SemanticFindImplementationsResponse{}, semanticError(CodeRequestFailed, "Failed to synchronize Go document before implementation lookup.", err)
	}
	position, err := lspPositionForPublic(doc, workspacesdk.SemanticPosition{Line: req.Target.Line, Column: req.Target.Column}, session.client.capabilities.positionEncoding)
	if err != nil {
		return workspacesdk.SemanticFindImplementationsResponse{}, err
	}
	raw, err := session.client.request(ctx, "textDocument/implementation", map[string]any{
		"textDocument": map[string]any{"uri": doc.uri},
		"position":     position,
	})
	if err != nil {
		return workspacesdk.SemanticFindImplementationsResponse{}, semanticRequestError("Find implementations request failed.", err)
	}
	locations, err := decodeSemanticLocations(raw)
	if err != nil {
		return workspacesdk.SemanticFindImplementationsResponse{}, semanticError(CodeRequestFailed, "Failed to decode implementation locations.", err)
	}
	implementations := make([]workspacesdk.SemanticImplementation, 0, len(locations))
	for _, location := range locations {
		implementation, ok := session.implementationFromLocation(location, req.ContextLines, scopePath)
		if ok {
			implementations = append(implementations, implementation)
		}
	}
	implementations = dedupeImplementations(implementations)
	sort.Slice(implementations, func(i, j int) bool {
		if implementations[i].Path != implementations[j].Path {
			return implementations[i].Path < implementations[j].Path
		}
		if implementations[i].Range.Start.Line != implementations[j].Range.Start.Line {
			return implementations[i].Range.Start.Line < implementations[j].Range.Start.Line
		}
		return implementations[i].Range.Start.Column < implementations[j].Range.Start.Column
	})
	observed := len(implementations)
	implementations, truncated := applyLimit(implementations, req.Limit)
	return workspacesdk.SemanticFindImplementationsResponse{
		Target:          workspacesdk.SemanticTarget{Path: path, Line: req.Target.Line, Column: req.Target.Column},
		Implementations: implementations,
		ReturnedCount:   len(implementations),
		ObservedCount:   observed,
		Truncated:       truncated,
		Coverage: workspacesdk.SemanticCoverage{
			Status: coverageUnknown,
			Reason: "Implementation completeness depends on the semantic backend index state.",
		},
	}, nil
}

func decodeSemanticLocations(raw json.RawMessage) ([]lspLocation, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return []lspLocation{}, nil
	}
	if strings.HasPrefix(trimmed, "{") {
		raw = json.RawMessage("[" + trimmed + "]")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	locations := make([]lspLocation, 0, len(items))
	for _, item := range items {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(item, &probe); err != nil {
			return nil, err
		}
		if _, ok := probe["targetUri"]; ok {
			var link lspLocationLink
			if err := json.Unmarshal(item, &link); err != nil {
				return nil, err
			}
			locations = append(locations, lspLocation{URI: link.TargetURI, Range: link.TargetSelectionRange})
			continue
		}
		var location lspLocation
		if err := json.Unmarshal(item, &location); err != nil {
			return nil, err
		}
		locations = append(locations, location)
	}
	return locations, nil
}

func (s *semanticSession) implementationFromLocation(location lspLocation, contextLines int, scopePath string) (workspacesdk.SemanticImplementation, bool) {
	reference, ok := s.referenceFromLocation(location, contextLines, scopePath)
	if !ok {
		return workspacesdk.SemanticImplementation{}, false
	}
	return workspacesdk.SemanticImplementation{
		Path:     reference.Path,
		Language: reference.Language,
		Range:    reference.Range,
		Locator:  reference.Locator,
		Context:  reference.Context,
	}, true
}

func dedupeImplementations(values []workspacesdk.SemanticImplementation) []workspacesdk.SemanticImplementation {
	seen := make(map[string]struct{}, len(values))
	out := make([]workspacesdk.SemanticImplementation, 0, len(values))
	for _, value := range values {
		key := fmt.Sprintf("%s:%d:%d:%d:%d", value.Path, value.Range.Start.Line, value.Range.Start.Column, value.Range.End.Line, value.Range.End.Column)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

type lspDocumentDiagnosticReport struct {
	Kind     string          `json:"kind"`
	ResultID string          `json:"resultId,omitempty"`
	Items    []lspDiagnostic `json:"items,omitempty"`
}

func (m *Manager) GetDiagnostics(ctx context.Context, req workspacesdk.SemanticDiagnosticsRequest) (workspacesdk.SemanticDiagnosticsResponse, error) {
	if len(req.Paths) == 0 || len(req.Paths) > maxDiagnosticsPaths {
		return workspacesdk.SemanticDiagnosticsResponse{}, semanticError(CodeInvalidPath, fmt.Sprintf("paths must contain between 1 and %d entries.", maxDiagnosticsPaths), nil)
	}
	if err := validateLimit(req.Limit); err != nil {
		return workspacesdk.SemanticDiagnosticsResponse{}, err
	}
	if err := validateContextLines(req.ContextLines); err != nil {
		return workspacesdk.SemanticDiagnosticsResponse{}, err
	}
	minSeverity, err := severityRank(req.MinimumSeverity)
	if err != nil {
		return workspacesdk.SemanticDiagnosticsResponse{}, err
	}

	files := make([]workspacesdk.SemanticDiagnosticFileStatus, 0, len(req.Paths))
	diagnostics := make([]workspacesdk.SemanticDiagnostic, 0)
	successes := 0
	freshnessUnknown := false
	commonError := error(nil)
	failureCodes := make(map[string]struct{})
	recordFailure := func(err error) {
		if err == nil {
			return
		}
		failureCodes[semanticErrorCode(err)] = struct{}{}
		if commonError == nil {
			commonError = err
		}
	}

	for _, requestedPath := range req.Paths {
		path, pathErr := canonicalExistingPath(requestedPath)
		if pathErr != nil {
			files = append(files, workspacesdk.SemanticDiagnosticFileStatus{
				Path: requestedPath, Status: statusError, Message: pathErr.Error(),
			})
			recordFailure(semanticError(CodeInvalidPath, "One or more diagnostic paths are invalid.", pathErr))
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() {
			if statErr == nil {
				statErr = xerrors.Errorf("%q is not a regular file", path)
			}
			files = append(files, workspacesdk.SemanticDiagnosticFileStatus{
				Path: path, Status: statusError, Message: statErr.Error(),
			})
			recordFailure(semanticError(CodeInvalidPath, "One or more diagnostic paths are invalid.", statErr))
			continue
		}
		if strings.ToLower(filepath.Ext(path)) != ".go" {
			files = append(files, workspacesdk.SemanticDiagnosticFileStatus{
				Path: path, Language: strings.ToLower(detectedLanguage(path)), Status: statusUnsupported,
				Message: "The current semantic implementation supports Go only.",
			})
			recordFailure(semanticError(CodeUnsupportedLanguage, "No supported semantic backend is available for the requested file language.", nil))
			continue
		}

		projectRoot, rootErr := goProjectRootForFile(path)
		if rootErr != nil {
			files = append(files, workspacesdk.SemanticDiagnosticFileStatus{Path: path, Language: languageGo, Status: statusError, Message: rootErr.Error()})
			recordFailure(semanticError(CodeInvalidPath, "Unable to determine Go semantic project root.", rootErr))
			continue
		}
		session, sessionErr := m.getSession(ctx, projectRoot)
		if sessionErr != nil {
			code := semanticErrorCode(sessionErr)
			status := statusError
			if code == CodeBackendNotReady {
				status = statusNotReady
			}
			files = append(files, workspacesdk.SemanticDiagnosticFileStatus{Path: path, Language: languageGo, Status: status, Message: sessionErr.Error()})
			recordFailure(sessionErr)
			continue
		}

		fileDiagnostics, freshness, diagErr := session.diagnosticsForFile(ctx, path, req.IncludeRelatedInformation, req.ContextLines)
		if diagErr != nil {
			code := semanticErrorCode(diagErr)
			status := statusError
			switch code {
			case CodeCapabilityUnsupported:
				status = statusCapabilityMissing
			case CodeBackendNotReady:
				status = statusNotReady
			}
			files = append(files, workspacesdk.SemanticDiagnosticFileStatus{Path: path, Language: languageGo, Status: status, Message: diagErr.Error()})
			recordFailure(diagErr)
			continue
		}

		filtered := make([]workspacesdk.SemanticDiagnostic, 0, len(fileDiagnostics))
		for _, diagnostic := range fileDiagnostics {
			rank, _ := severityRank(diagnostic.Severity)
			if rank <= minSeverity {
				filtered = append(filtered, diagnostic)
			}
		}
		diagnostics = append(diagnostics, filtered...)
		files = append(files, workspacesdk.SemanticDiagnosticFileStatus{
			Path: path, Language: languageGo, Status: statusOK, Freshness: freshness, DiagnosticCount: len(filtered),
		})
		successes++
		if freshness == "unknown" {
			freshnessUnknown = true
		}
	}

	if successes == 0 && commonError != nil && len(failureCodes) == 1 {
		return workspacesdk.SemanticDiagnosticsResponse{}, commonError
	}

	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Path != diagnostics[j].Path {
			return diagnostics[i].Path < diagnostics[j].Path
		}
		if diagnostics[i].Range.Start.Line != diagnostics[j].Range.Start.Line {
			return diagnostics[i].Range.Start.Line < diagnostics[j].Range.Start.Line
		}
		if diagnostics[i].Range.Start.Column != diagnostics[j].Range.Start.Column {
			return diagnostics[i].Range.Start.Column < diagnostics[j].Range.Start.Column
		}
		ri, _ := severityRank(diagnostics[i].Severity)
		rj, _ := severityRank(diagnostics[j].Severity)
		if ri != rj {
			return ri < rj
		}
		if diagnostics[i].Source != diagnostics[j].Source {
			return diagnostics[i].Source < diagnostics[j].Source
		}
		return diagnostics[i].Message < diagnostics[j].Message
	})
	observed := len(diagnostics)
	diagnostics, truncated := applyLimit(diagnostics, req.Limit)

	coverage := workspacesdk.SemanticCoverage{Status: coverageComplete}
	if successes != len(req.Paths) {
		coverage = workspacesdk.SemanticCoverage{Status: coveragePartial, Reason: "One or more requested files could not be semantically analyzed."}
	} else if freshnessUnknown {
		coverage = workspacesdk.SemanticCoverage{Status: coverageUnknown, Reason: "The backend did not provide enough version information to prove exact diagnostic freshness for every file."}
	}

	return workspacesdk.SemanticDiagnosticsResponse{
		Files: files, Diagnostics: diagnostics, ReturnedCount: len(diagnostics),
		ObservedCount: observed, Truncated: truncated, Coverage: coverage,
	}, nil
}

func semanticErrorCode(err error) string {
	var semanticErr *Error
	if errors.As(err, &semanticErr) {
		return semanticErr.Code
	}
	return CodeRequestFailed
}

func severityRank(severity string) (int, error) {
	switch severity {
	case "", "hint":
		return 4, nil
	case "information":
		return 3, nil
	case "warning":
		return 2, nil
	case "error":
		return 1, nil
	default:
		return 0, semanticError(CodeInvalidPath, "minimum_severity must be error, warning, information, or hint.", nil)
	}
}

func (s *semanticSession) diagnosticsForFile(
	ctx context.Context,
	path string,
	includeRelated bool,
	contextLines int,
) ([]workspacesdk.SemanticDiagnostic, string, error) {
	doc, err := s.syncDocument(ctx, path, syncIfChanged)
	if err != nil {
		return nil, "", semanticError(CodeRequestFailed, "Failed to synchronize Go document before diagnostics.", err)
	}

	var diagnostics []lspDiagnostic
	freshness := "fresh"
	if s.client.capabilities.pullDiagnostics {
		params := map[string]any{
			"textDocument": map[string]any{"uri": doc.uri},
		}
		s.pullDiagnosticsMu.Lock()
		previous, hasPrevious := s.pullDiagnostics[doc.uri]
		if hasPrevious && previous.version == doc.version && previous.resultID != "" {
			params["previousResultId"] = previous.resultID
		} else {
			hasPrevious = false
		}
		s.pullDiagnosticsMu.Unlock()

		var report lspDocumentDiagnosticReport
		if err := s.client.requestInto(ctx, "textDocument/diagnostic", params, &report); err != nil {
			return nil, "", semanticRequestError("Get diagnostics request failed.", err)
		}
		switch report.Kind {
		case "full", "":
			diagnostics = report.Items
			s.pullDiagnosticsMu.Lock()
			s.pullDiagnostics[doc.uri] = pullDiagnosticState{
				version:     doc.version,
				resultID:    report.ResultID,
				diagnostics: append([]lspDiagnostic(nil), report.Items...),
			}
			s.pullDiagnosticsMu.Unlock()
		case "unchanged":
			if !hasPrevious {
				return nil, "", semanticError(
					CodeBackendNotReady,
					"Go semantic backend returned unchanged diagnostics without a matching current snapshot.",
					nil,
				)
			}
			diagnostics = append([]lspDiagnostic(nil), previous.diagnostics...)
		default:
			return nil, "", semanticError(
				CodeRequestFailed,
				"Go semantic backend returned an unsupported diagnostic report kind.",
				xerrors.Errorf("kind %q", report.Kind),
			)
		}
	} else {
		if snapshot, ok := s.client.latestDiagnostics(doc.uri); ok && snapshot.version != nil && *snapshot.version == doc.version {
			diagnostics = snapshot.diagnostics
		} else {
			after := s.client.diagnosticSequence()
			doc, err = s.syncDocument(ctx, path, syncForceChange)
			if err != nil {
				return nil, "", semanticError(CodeRequestFailed, "Failed to refresh Go document before diagnostics.", err)
			}
			waitCtx, cancel := context.WithTimeout(ctx, diagnosticsWaitTimeout)
			snapshot, snapshotFreshness, waitErr := s.client.waitForDiagnostics(waitCtx, doc.uri, after, doc.version)
			cancel()
			if waitErr != nil {
				return nil, "", semanticError(CodeBackendNotReady, "Go semantic diagnostics were not ready before the diagnostic wait deadline.", waitErr)
			}
			diagnostics = snapshot.diagnostics
			freshness = snapshotFreshness
		}
	}

	out := make([]workspacesdk.SemanticDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		publicDiagnostic, ok := s.publicDiagnostic(path, doc.lines, diagnostic, diagnosticRenderOptions{includeRelated: includeRelated, contextLines: contextLines})
		if ok {
			out = append(out, publicDiagnostic)
		}
	}
	return out, freshness, nil
}

type diagnosticRenderOptions struct {
	includeRelated bool
	contextLines   int
}

func (s *semanticSession) publicDiagnostic(
	path string,
	lines []string,
	diagnostic lspDiagnostic,
	options diagnosticRenderOptions,
) (workspacesdk.SemanticDiagnostic, bool) {
	publicRange, err := publicRangeFromLSP(path, lines, diagnostic.Range, s.client.capabilities.positionEncoding)
	if err != nil {
		return workspacesdk.SemanticDiagnostic{}, false
	}
	result := workspacesdk.SemanticDiagnostic{
		Path: path, Severity: diagnosticSeverity(diagnostic.Severity), Message: diagnostic.Message,
		Range: publicRange, Source: diagnostic.Source, Code: diagnosticCode(diagnostic.Code),
		Context: sourceContext(lines, publicRange, options.contextLines),
	}
	if diagnostic.CodeDescription != nil {
		result.CodeHref = diagnostic.CodeDescription.Href
	}
	for _, tag := range diagnostic.Tags {
		switch tag {
		case 1:
			result.Tags = append(result.Tags, "unnecessary")
		case 2:
			result.Tags = append(result.Tags, "deprecated")
		}
	}
	if options.includeRelated {
		for _, related := range diagnostic.RelatedInformation {
			relatedPath, err := fileURIToPath(related.Location.URI)
			if err != nil {
				continue
			}
			relatedPath, err = canonicalExistingPath(relatedPath)
			if err != nil {
				continue
			}
			data, err := readSemanticSource(relatedPath)
			if err != nil {
				continue
			}
			relatedRange, err := publicRangeFromLSP(relatedPath, splitSourceLines(string(data)), related.Location.Range, s.client.capabilities.positionEncoding)
			if err != nil {
				continue
			}
			result.RelatedInformation = append(result.RelatedInformation, workspacesdk.SemanticRelatedInformation{
				Path: relatedPath, Range: relatedRange, Message: related.Message,
			})
		}
	}
	return result, true
}

func diagnosticSeverity(severity int) string {
	switch severity {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 4:
		return "hint"
	case 3:
		return "information"
	default:
		return "information"
	}
}

func diagnosticCode(code any) string {
	switch value := code.(type) {
	case string:
		return value
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case json.Number:
		return value.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(value)
	}
}
