// Package agentgit provides a WebSocket-based service for watching git
// repository changes on the agent. It is mounted at /api/v0/git/watch
// and allows clients to subscribe to file paths, triggering scans of
// the corresponding git repositories.
package agentgit

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/quartz"
)

// Option configures the git watch service.
type Option func(*Handler)

// WithClock sets a controllable clock for testing. Defaults to
// quartz.NewReal().
func WithClock(c quartz.Clock) Option {
	return func(h *Handler) {
		h.clock = c
	}
}

// WithGitBinary overrides the git binary path (for testing).
func WithGitBinary(path string) Option {
	return func(h *Handler) {
		h.gitBin = path
	}
}

// WithWorkingDirectory provides the agent workspace directory used for
// initial Git discovery when no chat paths have been observed yet.
func WithWorkingDirectory(fn func() string) Option {
	return func(h *Handler) {
		h.workingDirectory = fn
	}
}

const (
	// scanCooldown is the minimum interval between successive scans.
	scanCooldown = 1 * time.Second
	// fallbackPollInterval is the safety-net poll period used when no
	// filesystem events arrive. scanCooldown caps the actual scan
	// frequency; an outer guard in RunLoop further skips the tick
	// when a trigger-driven scan already ran within this interval.
	// Each tick uses a temporary Git index and a single streaming diff
	// process per subscribed repository.
	fallbackPollInterval = 5 * time.Second
	// maxLegacySnapshotSize bounds only the final compatibility snapshot sent to
	// clients that ignore progressive messages. Progressive clients receive the
	// complete diff without a total-size limit.
	maxLegacySnapshotSize = 3 * 1024 * 1024 // 3 MiB
	// progressEmitInterval and progressChunkSize keep the UI responsive without
	// flooding the WebSocket with one message per file.
	progressEmitInterval = 100 * time.Millisecond
	progressChunkSize    = 16 * 1024
)

// Handler manages per-connection git watch state.
type Handler struct {
	logger           slog.Logger
	clock            quartz.Clock
	gitBin           string // path to git binary; empty means "git" (from PATH)
	workingDirectory func() string

	mu             sync.Mutex
	repoRoots      map[string]struct{}     // watched repo roots
	lastSnapshots  map[string]repoSnapshot // last emitted snapshot per repo
	lastScanAt     time.Time               // when the last scan completed
	scanTrigger    chan struct{}           // buffered(1), poked by triggers
	scanCancel     context.CancelFunc      // cancels the currently running progressive scan
	scanGeneration uint64                  // identifies the active progressive scan
}

// repoSnapshot captures the last emitted state for delta comparison.
type repoSnapshot struct {
	branch        string
	remoteOrigin  string
	unifiedDiff   string
	diffTruncated bool
	fingerprint   [sha256.Size]byte
}

// NewHandler creates a new git watch handler.
func NewHandler(logger slog.Logger, opts ...Option) *Handler {
	h := &Handler{
		logger:        logger,
		clock:         quartz.NewReal(),
		gitBin:        "git",
		repoRoots:     make(map[string]struct{}),
		lastSnapshots: make(map[string]repoSnapshot),
		scanTrigger:   make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(h)
	}

	// Check if git is available.
	if _, err := exec.LookPath(h.gitBin); err != nil {
		h.logger.Warn(context.Background(), "git binary not found, git scanning disabled")
	}

	return h
}

// gitAvailable returns true if the configured git binary can be found
// in PATH.
func (h *Handler) gitAvailable() bool {
	_, err := exec.LookPath(h.gitBin)
	return err == nil
}

// Subscribe processes a subscribe message, resolving paths to git repo
// roots and adding new repos to the watch set. Returns true if any new
// repo roots were added.
func (h *Handler) Subscribe(paths []string) bool {
	if !h.gitAvailable() {
		return false
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	added := false
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			continue
		}
		p = filepath.Clean(p)

		root, err := findRepoRoot(h.gitBin, p)
		if err != nil {
			// Not a git path — silently ignore.
			continue
		}
		if _, ok := h.repoRoots[root]; ok {
			continue
		}
		h.repoRoots[root] = struct{}{}
		added = true
	}
	return added
}

// SubscribeWorkingDirectory resolves the configured agent workspace directory
// to a Git repository. It is used as a durable discovery fallback when the
// in-memory PathStore is empty, for example after an agent restart.
func (h *Handler) SubscribeWorkingDirectory() bool {
	if h.workingDirectory == nil {
		return false
	}
	dir := strings.TrimSpace(h.workingDirectory())
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	return h.Subscribe([]string{dir})
}

// HasSubscriptions reports whether at least one repository root is currently
// watched by this connection.
func (h *Handler) HasSubscriptions() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.repoRoots) > 0
}

// RequestScan pokes the scan trigger so the run loop performs a scan.
func (h *Handler) RequestScan() {
	h.mu.Lock()
	cancel := h.scanCancel
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	select {
	case h.scanTrigger <- struct{}{}:
	default:
		// Already pending.
	}
}

// Scan performs a scan of all subscribed repos and computes deltas
// against the previously emitted snapshots. It is retained for callers
// that need one final snapshot instead of incremental progress.
func (h *Handler) Scan(ctx context.Context) *codersdk.WorkspaceAgentGitServerMessage {
	if !h.gitAvailable() {
		return nil
	}

	roots := h.subscribedRoots()
	if len(roots) == 0 {
		return nil
	}

	now := h.clock.Now().UTC()
	results := make([]scanResult, 0, len(roots))
	for _, root := range roots {
		changes, err := getRepoChanges(ctx, h.logger, h.gitBin, root)
		res := scanResult{root: root, changes: changes, err: err}
		if err == nil {
			if fingerprint, fingerprintErr := computeRepoFingerprint(ctx, h.gitBin, root); fingerprintErr == nil {
				res.fingerprint = fingerprint
			}
		}
		results = append(results, res)
	}

	return h.commitScanResults(ctx, now, results)
}

// ScanProgressive scans all subscribed repositories while emitting incremental
// diff progress. A refresh or path update cancels the active scan so stale work
// never blocks a newer snapshot.
func (h *Handler) ScanProgressive(
	ctx context.Context,
	emit func(codersdk.WorkspaceAgentGitServerMessage) error,
) *codersdk.WorkspaceAgentGitServerMessage {
	if !h.gitAvailable() {
		return nil
	}

	roots := h.subscribedRoots()
	if len(roots) == 0 {
		return nil
	}

	scanCtx, finish := h.beginProgressiveScan(ctx)
	defer finish()

	now := h.clock.Now().UTC()
	results := make([]scanResult, 0, len(roots))
	for _, root := range roots {
		if scanCtx.Err() != nil {
			return nil
		}

		changes, err := getRepoMetadata(scanCtx, h.logger, h.gitBin, root)
		if err != nil {
			results = append(results, scanResult{root: root, changes: changes, err: err})
			continue
		}

		// Publish repository context before any potentially expensive diff setup.
		// TotalFiles is filled by the next progress update once Git has counted it.
		if err := emit(codersdk.WorkspaceAgentGitServerMessage{
			Type: codersdk.WorkspaceAgentGitServerMessageTypeProgress,
			Progress: &codersdk.WorkspaceAgentGitDiffProgress{
				RepoRoot:     root,
				Branch:       changes.Branch,
				RemoteOrigin: changes.RemoteOrigin,
				Reset:        true,
			},
		}); err != nil {
			return nil
		}

		diff, truncated, err := computeGitDiffProgressive(
			scanCtx,
			h.logger,
			h.gitBin,
			root,
			func(update gitDiffProgressUpdate) error {
				return emit(codersdk.WorkspaceAgentGitServerMessage{
					Type: codersdk.WorkspaceAgentGitServerMessageTypeProgress,
					Progress: &codersdk.WorkspaceAgentGitDiffProgress{
						RepoRoot:         root,
						Branch:           changes.Branch,
						RemoteOrigin:     changes.RemoteOrigin,
						UnifiedDiffChunk: update.chunk,
						ProcessedFiles:   update.processedFiles,
						TotalFiles:       update.totalFiles,
						Complete:         update.complete,
					},
				})
			},
		)
		if err != nil {
			if scanCtx.Err() != nil {
				return nil
			}
			results = append(results, scanResult{root: root, changes: changes, err: err})
			continue
		}

		changes.UnifiedDiff = diff
		changes.DiffTruncated = truncated
		res := scanResult{root: root, changes: changes}
		if fingerprint, fingerprintErr := computeRepoFingerprint(scanCtx, h.gitBin, root); fingerprintErr == nil {
			res.fingerprint = fingerprint
		}
		results = append(results, res)
	}

	if scanCtx.Err() != nil {
		return nil
	}
	return h.commitScanResults(scanCtx, now, results)
}

type scanResult struct {
	root        string
	changes     codersdk.WorkspaceAgentRepoChanges
	fingerprint [sha256.Size]byte
	err         error
}

// NeedsProgressiveScan performs the lightweight fallback-poll check. It hashes
// Git status plus metadata for modified/untracked worktree paths, which catches
// normal editor writes without generating any file diffs. A changed fingerprint
// asks RunLoop to schedule a fresh progressive scan.
func (h *Handler) NeedsProgressiveScan(ctx context.Context) bool {
	if !h.gitAvailable() {
		return false
	}
	for _, root := range h.subscribedRoots() {
		changes, err := getRepoMetadata(ctx, h.logger, h.gitBin, root)
		if err != nil {
			if isRepoDeleted(h.gitBin, root) {
				return true
			}
			h.logger.Debug(ctx, "fallback git metadata check failed",
				slog.F("root", root), slog.Error(err))
			continue
		}
		fingerprint, err := computeRepoFingerprint(ctx, h.gitBin, root)
		if err != nil {
			h.logger.Debug(ctx, "fallback git fingerprint failed",
				slog.F("root", root), slog.Error(err))
			continue
		}

		h.mu.Lock()
		prev, ok := h.lastSnapshots[root]
		h.mu.Unlock()
		if !ok || prev.branch != changes.Branch || prev.remoteOrigin != changes.RemoteOrigin || prev.fingerprint != fingerprint {
			return true
		}
	}
	return false
}

func (h *Handler) subscribedRoots() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	roots := make([]string, 0, len(h.repoRoots))
	for root := range h.repoRoots {
		roots = append(roots, root)
	}
	return roots
}

func (h *Handler) beginProgressiveScan(parent context.Context) (context.Context, func()) {
	scanCtx, cancel := context.WithCancel(parent)

	h.mu.Lock()
	previousCancel := h.scanCancel
	h.scanGeneration++
	generation := h.scanGeneration
	h.scanCancel = cancel
	h.mu.Unlock()

	if previousCancel != nil {
		previousCancel()
	}

	return scanCtx, func() {
		cancel()
		h.mu.Lock()
		if h.scanGeneration == generation {
			h.scanCancel = nil
		}
		h.mu.Unlock()
	}
}

func (h *Handler) commitScanResults(
	ctx context.Context,
	now time.Time,
	results []scanResult,
) *codersdk.WorkspaceAgentGitServerMessage {
	var repos []codersdk.WorkspaceAgentRepoChanges

	h.mu.Lock()
	defer h.mu.Unlock()

	for _, res := range results {
		if res.err != nil {
			if isRepoDeleted(h.gitBin, res.root) {
				removal := codersdk.WorkspaceAgentRepoChanges{
					RepoRoot: res.root,
					Removed:  true,
				}
				delete(h.repoRoots, res.root)
				delete(h.lastSnapshots, res.root)
				repos = append(repos, removal)
			} else {
				h.logger.Warn(ctx, "scan repo failed",
					slog.F("root", res.root),
					slog.Error(res.err),
				)
			}
			continue
		}

		prev, hasPrev := h.lastSnapshots[res.root]
		if hasPrev &&
			prev.branch == res.changes.Branch &&
			prev.remoteOrigin == res.changes.RemoteOrigin &&
			prev.unifiedDiff == res.changes.UnifiedDiff &&
			prev.diffTruncated == res.changes.DiffTruncated &&
			prev.fingerprint == res.fingerprint {
			continue
		}

		h.lastSnapshots[res.root] = repoSnapshot{
			branch:        res.changes.Branch,
			remoteOrigin:  res.changes.RemoteOrigin,
			unifiedDiff:   res.changes.UnifiedDiff,
			diffTruncated: res.changes.DiffTruncated,
			fingerprint:   res.fingerprint,
		}
		repos = append(repos, res.changes)
	}

	h.lastScanAt = now
	return &codersdk.WorkspaceAgentGitServerMessage{
		Type:         codersdk.WorkspaceAgentGitServerMessageTypeChanges,
		ScannedAt:    &now,
		Repositories: repos,
	}
}

// RunLoop runs the main event loop that listens for refresh requests and
// fallback poll ticks. Trigger-driven scans may stream progress; fallback scans
// use a quiet snapshot function so an unchanged large diff is not re-streamed
// to the browser every polling interval. Both are rate-limited by scanCooldown.
func (h *Handler) RunLoop(ctx context.Context, scanFn func(), fallbackScanFn func()) {
	fallbackTicker := h.clock.NewTicker(fallbackPollInterval)
	defer fallbackTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-h.scanTrigger:
			h.rateLimitedScan(ctx, scanFn)

		case <-fallbackTicker.C:
			// Skip when a recent trigger-driven scan already covered
			// this interval, so a busy chat pays near-zero poll cost.
			h.mu.Lock()
			recent := !h.lastScanAt.IsZero() &&
				h.clock.Since(h.lastScanAt) < fallbackPollInterval
			h.mu.Unlock()
			if recent {
				continue
			}
			h.rateLimitedScan(ctx, fallbackScanFn)
		}
	}
}

func (h *Handler) rateLimitedScan(ctx context.Context, scanFn func()) {
	h.mu.Lock()
	elapsed := h.clock.Since(h.lastScanAt)
	if elapsed < scanCooldown {
		h.mu.Unlock()

		// Wait for cooldown then scan.
		remaining := scanCooldown - elapsed
		timer := h.clock.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		scanFn()
		return
	}
	h.mu.Unlock()
	scanFn()
}

// isRepoDeleted returns true when the repo root directory or its .git
// entry no longer represents a valid git repository. This
// distinguishes a genuine repo deletion from a transient scan error
// (e.g. lock contention).
//
// It handles three deletion cases:
//  1. The repo root directory itself was removed.
//  2. The .git entry (directory or file) was removed.
//  3. The .git entry is a file (worktree/submodule) whose target
//     gitdir was removed. In this case .git exists on disk but
//     `git rev-parse --git-dir` fails because the referenced
//     directory is gone.
func isRepoDeleted(gitBin string, repoRoot string) bool {
	if _, err := os.Stat(repoRoot); os.IsNotExist(err) {
		return true
	}
	gitPath := filepath.Join(repoRoot, ".git")
	fi, err := os.Stat(gitPath)
	if os.IsNotExist(err) {
		return true
	}
	// If .git is a regular file (worktree or submodule), the actual
	// git object store lives elsewhere. Validate that the target is
	// still reachable by running git rev-parse.
	if err == nil && !fi.IsDir() {
		cmd := exec.CommandContext(context.Background(), gitBin, "-C", repoRoot, "rev-parse", "--git-dir")
		if err := cmd.Run(); err != nil {
			return true
		}
	}
	return false
}

// findRepoRoot uses `git rev-parse --show-toplevel` to find the
// repository root for the given path.
func findRepoRoot(gitBin string, p string) (string, error) {
	// If p is a file, start from its parent directory.
	dir := p
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		dir = filepath.Dir(dir)
	}
	cmd := exec.CommandContext(context.Background(), gitBin, "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", xerrors.Errorf("no git repo found for %s", p)
	}
	root := filepath.FromSlash(strings.TrimSpace(string(out)))
	// Resolve symlinks and short (8.3) names on Windows so the
	// returned root matches paths produced by Go's filepath APIs.
	if resolved, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		root = resolved
	}
	return root, nil
}

// getRepoChanges reads the current state of a git repository using
// the git CLI. It returns branch, remote origin, and a unified diff.
func getRepoChanges(ctx context.Context, logger slog.Logger, gitBin string, repoRoot string) (codersdk.WorkspaceAgentRepoChanges, error) {
	result, err := getRepoMetadata(ctx, logger, gitBin, repoRoot)
	if err != nil {
		return result, err
	}

	diff, truncated, err := computeGitDiffProgressive(ctx, logger, gitBin, repoRoot, nil)
	if err != nil {
		return result, xerrors.Errorf("compute diff: %w", err)
	}
	result.UnifiedDiff = diff
	result.DiffTruncated = truncated
	return result, nil
}

func getRepoMetadata(ctx context.Context, logger slog.Logger, gitBin string, repoRoot string) (codersdk.WorkspaceAgentRepoChanges, error) {
	result := codersdk.WorkspaceAgentRepoChanges{RepoRoot: repoRoot}

	verifyCmd := exec.CommandContext(ctx, gitBin, "-C", repoRoot, "rev-parse", "--git-dir")
	if err := verifyCmd.Run(); err != nil {
		return result, xerrors.Errorf("not a git repository: %w", err)
	}

	branchCmd := exec.CommandContext(ctx, gitBin, "-C", repoRoot, "symbolic-ref", "--short", "HEAD")
	if out, err := branchCmd.Output(); err == nil {
		result.Branch = strings.TrimSpace(string(out))
	} else {
		logger.Debug(ctx, "failed to read HEAD", slog.F("root", repoRoot), slog.Error(err))
	}

	remoteCmd := exec.CommandContext(ctx, gitBin, "-C", repoRoot, "config", "--get", "remote.origin.url")
	if out, err := remoteCmd.Output(); err == nil {
		result.RemoteOrigin = strings.TrimSpace(string(out))
	}
	return result, nil
}

type gitDiffProgressUpdate struct {
	chunk          string
	processedFiles int
	totalFiles     int
	complete       bool
}

type gitDiffProgressFunc func(gitDiffProgressUpdate) error

// computeGitDiffProgressive builds a complete working-tree diff without
// mutating the real Git index. Untracked paths are marked intent-to-add in a
// temporary index, allowing one streaming `git diff` process to cover tracked,
// staged, and untracked changes. Progress is emitted incrementally in bounded
// chunks while file counters advance at diff-section boundaries.
func computeGitDiffProgressive(
	ctx context.Context,
	logger slog.Logger,
	gitBin string,
	repoRoot string,
	emit gitDiffProgressFunc,
) (string, bool, error) {
	hasHead := exec.CommandContext(ctx, gitBin, "-C", repoRoot, "rev-parse", "--verify", "HEAD").Run() == nil

	trackedCount := 0
	var untrackedPaths []string
	if hasHead {
		trackedNamesCmd := exec.CommandContext(ctx, gitBin, "-C", repoRoot,
			"diff", "--no-ext-diff", "--no-textconv", "HEAD", "--name-only", "-z", "--")
		trackedNames, err := trackedNamesCmd.Output()
		if err != nil {
			return "", false, xerrors.Errorf("list tracked changes: %w", err)
		}
		trackedCount = bytes.Count(trackedNames, []byte{0})

		untrackedCmd := exec.CommandContext(ctx, gitBin, "-C", repoRoot,
			"ls-files", "--others", "--exclude-standard", "-z")
		untracked, err := untrackedCmd.Output()
		if err != nil {
			return "", false, xerrors.Errorf("list untracked files: %w", err)
		}
		untrackedPaths = splitNullPaths(untracked)
	} else {
		// With no HEAD, staged files and ordinary untracked files are both new
		// content. Process them through the same empty-index batches.
		untrackedCmd := exec.CommandContext(ctx, gitBin, "-C", repoRoot,
			"ls-files", "--cached", "--others", "--exclude-standard", "-z")
		untracked, err := untrackedCmd.Output()
		if err != nil {
			return "", false, xerrors.Errorf("list files in unborn repository: %w", err)
		}
		untrackedPaths = splitNullPaths(untracked)
	}

	totalFiles := trackedCount + len(untrackedPaths)
	collector := newGitDiffCollector(totalFiles, emit)
	if emit != nil {
		if err := collector.emitUpdate(false); err != nil {
			return "", false, err
		}
	}
	if totalFiles == 0 {
		if emit != nil {
			if err := collector.emitUpdate(true); err != nil {
				return "", false, err
			}
		}
		return "", false, nil
	}

	// Tracked/staged changes are available immediately from one ordinary diff;
	// no temporary index is required and the real index remains read-only.
	if hasHead && trackedCount > 0 {
		trackedCmd := exec.CommandContext(ctx, gitBin, "-C", repoRoot,
			"diff", "--no-ext-diff", "--no-textconv", "HEAD", "--")
		if err := collector.streamCommand(ctx, trackedCmd); err != nil {
			return "", collector.legacyTruncated, xerrors.Errorf("stream tracked diff: %w", err)
		}
		if err := collector.advanceTo(trackedCount); err != nil {
			return "", collector.legacyTruncated, err
		}
	}

	// Process untracked files in bounded batches. Each batch uses an empty
	// disposable index with intent-to-add entries, so the first untracked diffs
	// arrive quickly even when the repository contains tens of thousands of
	// files. There is intentionally no total file-count limit.
	for offset := 0; offset < len(untrackedPaths); offset += untrackedBatchSize {
		if err := ctx.Err(); err != nil {
			return "", collector.legacyTruncated, err
		}
		end := min(offset+untrackedBatchSize, len(untrackedPaths))
		batch := untrackedPaths[offset:end]
		diffCmd, cleanup, err := prepareUntrackedBatchDiff(ctx, logger, gitBin, repoRoot, batch)
		if err != nil {
			return "", collector.legacyTruncated, err
		}
		err = collector.streamCommand(ctx, diffCmd)
		cleanup()
		if err != nil {
			return "", collector.legacyTruncated, xerrors.Errorf("stream untracked diff: %w", err)
		}
		if err := collector.advanceTo(trackedCount + end); err != nil {
			return "", collector.legacyTruncated, err
		}
	}

	collector.processedFiles = totalFiles
	if emit != nil {
		if err := collector.emitUpdate(true); err != nil {
			return "", collector.legacyTruncated, err
		}
	}
	return collector.legacySnapshot.String(), collector.legacyTruncated, nil
}

const untrackedBatchSize = 256

type gitDiffCollector struct {
	emit gitDiffProgressFunc

	totalFiles      int
	processedFiles  int
	lastSentFiles   int
	pending         bytes.Buffer
	legacySnapshot  bytes.Buffer
	legacySection   bytes.Buffer
	legacyTruncated bool
	legacyStopped   bool
	lastEmit        time.Time
}

func newGitDiffCollector(totalFiles int, emit gitDiffProgressFunc) *gitDiffCollector {
	return &gitDiffCollector{
		emit:          emit,
		totalFiles:    totalFiles,
		lastSentFiles: -1,
	}
}

func (c *gitDiffCollector) emitUpdate(complete bool) error {
	if c.emit == nil {
		c.pending.Reset()
		c.lastSentFiles = c.processedFiles
		return nil
	}
	update := gitDiffProgressUpdate{
		chunk:          c.pending.String(),
		processedFiles: c.processedFiles,
		totalFiles:     c.totalFiles,
		complete:       complete,
	}
	c.pending.Reset()
	if err := c.emit(update); err != nil {
		return err
	}
	c.lastSentFiles = c.processedFiles
	c.lastEmit = time.Now()
	return nil
}

func (c *gitDiffCollector) finishLegacySection() {
	if c.legacySection.Len() == 0 {
		return
	}
	if !c.legacyStopped {
		if c.legacySnapshot.Len()+c.legacySection.Len() <= maxLegacySnapshotSize {
			_, _ = c.legacySnapshot.Write(c.legacySection.Bytes())
		} else {
			c.legacyTruncated = true
			c.legacyStopped = true
		}
	}
	c.legacySection.Reset()
}

func (c *gitDiffCollector) finishFile() error {
	c.processedFiles++
	c.finishLegacySection()
	if c.emit != nil && (c.pending.Len() >= progressChunkSize || c.lastEmit.IsZero() || time.Since(c.lastEmit) >= progressEmitInterval) {
		return c.emitUpdate(false)
	}
	return nil
}

func (c *gitDiffCollector) writeLine(line string) error {
	// Progressive clients receive the complete stream without a total-size cap.
	if c.emit != nil {
		_, _ = c.pending.WriteString(line)
	}

	// The final compatibility snapshot is bounded, but only at complete file
	// boundaries so legacy clients never receive a malformed partial patch.
	if !c.legacyStopped {
		if c.legacySnapshot.Len()+c.legacySection.Len()+len(line) <= maxLegacySnapshotSize {
			_, _ = c.legacySection.WriteString(line)
		} else {
			c.legacyTruncated = true
			c.legacyStopped = true
			c.legacySection.Reset()
		}
	}

	if c.emit != nil && (c.pending.Len() >= progressChunkSize || c.lastEmit.IsZero() || time.Since(c.lastEmit) >= progressEmitInterval) {
		return c.emitUpdate(false)
	}
	return nil
}

func (c *gitDiffCollector) advanceTo(target int) error {
	if target > c.processedFiles {
		c.processedFiles = target
	}
	if c.emit != nil && c.lastSentFiles != c.processedFiles {
		return c.emitUpdate(false)
	}
	return nil
}

func (c *gitDiffCollector) streamCommand(ctx context.Context, cmd *exec.Cmd) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return xerrors.Errorf("open git diff stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return xerrors.Errorf("start git diff: %w", err)
	}

	reader := bufio.NewReader(stdout)
	seenFile := false
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			if strings.HasPrefix(line, "diff --git ") {
				if seenFile {
					if err := c.finishFile(); err != nil {
						_ = cmd.Process.Kill()
						_ = cmd.Wait()
						return err
					}
				}
				seenFile = true
			}
			if err := c.writeLine(line); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return err
			}
		}

		if readErr == io.EOF {
			if seenFile {
				if err := c.finishFile(); err != nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					return err
				}
			}
			break
		}
		if readErr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return xerrors.Errorf("read git diff: %w", readErr)
		}
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return xerrors.Errorf("git diff: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func splitNullPaths(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{0})
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			paths = append(paths, string(part))
		}
	}
	return paths
}

func computeRepoFingerprint(ctx context.Context, gitBin, repoRoot string) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	hash := sha256.New()

	statusCmd := gitReadOnlyCommand(ctx, gitBin, repoRoot,
		"status", "--porcelain=v1", "-z", "--untracked-files=all")
	status, err := statusCmd.Output()
	if err != nil {
		return zero, xerrors.Errorf("git status fingerprint: %w", err)
	}
	_, _ = hash.Write(status)

	// Include staged blob identities. Porcelain status alone only says that a
	// path is staged and would miss a second staged edit to the same path.
	cachedCmd := gitReadOnlyCommand(ctx, gitBin, repoRoot,
		"diff", "--cached", "--raw", "-z", "--")
	cached, err := cachedCmd.Output()
	if err != nil {
		return zero, xerrors.Errorf("git cached fingerprint: %w", err)
	}
	_, _ = hash.Write(cached)

	// For dirty worktree/untracked files, include filesystem metadata so an edit
	// to an already-dirty path changes the fingerprint even when porcelain status
	// remains " M" or "??". Normal editors update mtime on each write.
	pathsCmd := gitReadOnlyCommand(ctx, gitBin, repoRoot,
		"ls-files", "--modified", "--others", "--exclude-standard", "-z")
	pathsOut, err := pathsCmd.Output()
	if err != nil {
		return zero, xerrors.Errorf("git worktree fingerprint paths: %w", err)
	}
	for _, rel := range splitNullPaths(pathsOut) {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(rel))
		info, statErr := os.Lstat(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if os.IsNotExist(statErr) {
			_, _ = hash.Write([]byte("\x00missing"))
			continue
		}
		if statErr != nil {
			return zero, xerrors.Errorf("stat fingerprint path %q: %w", rel, statErr)
		}
		_, _ = fmt.Fprintf(hash, "\x00%d:%d:%d", info.Size(), info.ModTime().UnixNano(), info.Mode())
		if info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(filepath.Join(repoRoot, filepath.FromSlash(rel)))
			if readErr != nil {
				return zero, xerrors.Errorf("read symlink fingerprint %q: %w", rel, readErr)
			}
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write([]byte(target))
		}
	}

	var sum [sha256.Size]byte
	copy(sum[:], hash.Sum(nil))
	return sum, nil
}

func gitReadOnlyCommand(ctx context.Context, gitBin, repoRoot string, args ...string) *exec.Cmd {
	commandArgs := append([]string{"-C", repoRoot}, args...)
	cmd := exec.CommandContext(ctx, gitBin, commandArgs...)
	prefix := "GIT_OPTIONAL_LOCKS="
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, prefix) {
			env = append(env, entry)
		}
	}
	cmd.Env = append(env, prefix+"0")
	return cmd
}

func prepareUntrackedBatchDiff(
	ctx context.Context,
	logger slog.Logger,
	gitBin string,
	repoRoot string,
	paths []string,
) (*exec.Cmd, func(), error) {
	tmp, err := os.CreateTemp("", "coder-git-diff-index-*")
	if err != nil {
		return nil, nil, xerrors.Errorf("create temporary git index: %w", err)
	}
	indexPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(indexPath)
		return nil, nil, xerrors.Errorf("close temporary git index: %w", err)
	}
	if err := os.Remove(indexPath); err != nil {
		return nil, nil, xerrors.Errorf("prepare temporary git index: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(indexPath)
		_ = os.Remove(indexPath + ".lock")
	}

	readTree := gitCommandWithIndex(ctx, gitBin, repoRoot, indexPath, "read-tree", "--empty")
	if err := readTree.Run(); err != nil {
		cleanup()
		return nil, nil, xerrors.Errorf("initialize temporary git index: %w", err)
	}

	var pathspec bytes.Buffer
	for _, path := range paths {
		_, _ = pathspec.WriteString(path)
		_ = pathspec.WriteByte(0)
	}
	addCmd := gitCommandWithIndex(ctx, gitBin, repoRoot, indexPath,
		"add", "-N", "--pathspec-from-file=-", "--pathspec-file-nul")
	addCmd.Stdin = &pathspec
	var addErr bytes.Buffer
	addCmd.Stderr = &addErr
	if err := addCmd.Run(); err != nil {
		cleanup()
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		logger.Debug(ctx, "failed to prepare untracked batch for progressive diff",
			slog.F("root", repoRoot), slog.Error(err))
		return nil, nil, xerrors.Errorf("prepare untracked batch: %w: %s", err, strings.TrimSpace(addErr.String()))
	}

	diffCmd := gitCommandWithIndex(ctx, gitBin, repoRoot, indexPath,
		"diff", "--no-ext-diff", "--no-textconv", "--")
	return diffCmd, cleanup, nil
}

func gitCommandWithIndex(ctx context.Context, gitBin, repoRoot, indexPath string, args ...string) *exec.Cmd {
	commandArgs := append([]string{"-C", repoRoot}, args...)
	cmd := exec.CommandContext(ctx, gitBin, commandArgs...)
	prefix := "GIT_INDEX_FILE="
	env := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, prefix) {
			env = append(env, entry)
		}
	}
	cmd.Env = append(env, prefix+indexPath)
	return cmd
}
