package agentproc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/afero"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/agent/agentexec"
	"github.com/coder/coder/v2/agent/usershell"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/quartz"
)

var (
	errProcessNotFound   = xerrors.New("process not found")
	errProcessNotRunning = xerrors.New("process is not running")

	// exitedProcessReapAge is how long an exited process is
	// kept before being automatically removed from the map.
	exitedProcessReapAge = 5 * time.Minute
)

func normalizeCommandActivityTool(tool string) string {
	switch strings.TrimSpace(tool) {
	case "exec":
		return "exec"
	case "bash", "execute_shell_command":
		return "execute_shell_command"
	case "process_start", "start_process":
		return "start_process"
	default:
		return ""
	}
}

type processActivityWriter struct {
	mu       sync.Mutex
	process  io.Writer
	activity io.Writer
}

func (w *processActivityWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = w.process.Write(p)
	_, _ = w.activity.Write(p)
	return len(p), nil
}

type deferredActivityWriter struct {
	mu      sync.Mutex
	pending bytes.Buffer
	sink    io.Writer
}

func (w *deferredActivityWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sink == nil {
		_, _ = w.pending.Write(p)
		return len(p), nil
	}
	_, _ = w.sink.Write(p)
	return len(p), nil
}

func (w *deferredActivityWriter) setSink(sink io.Writer) {
	if sink == nil {
		sink = io.Discard
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sink = sink
	if w.pending.Len() > 0 {
		_, _ = w.sink.Write(w.pending.Bytes())
		w.pending.Reset()
	}
}

func normalizeCommandActivitySource(tool, chatID string) string {
	// Channel attribution is derived from trusted request context rather than a
	// caller-provided source label. Chat identity is carried by agentchat
	// middleware; MCP attribution requires one of the allowlisted tool names.
	if chatID != "" {
		return "chat"
	}
	if tool != "" {
		return "mcp"
	}
	return "agentproc"
}

// process represents a running or completed process.
type process struct {
	mu            sync.Mutex
	inputMu       sync.Mutex
	id            string
	command       string
	argv          []string
	workDir       string
	host          string
	port          int
	identityFile  string
	remotePIDFile string
	tool          string
	fingerprint   string
	background    bool
	interactive   bool
	chatID        string
	dedupeScope   string
	cmd           *exec.Cmd
	stdin         io.WriteCloser
	stdinClosed   bool
	cancel        context.CancelFunc
	buf           *HeadTailBuffer
	logger        slog.Logger
	running       bool
	exitCode      *int
	startedAt     int64
	exitedAt      *int64
	done          chan struct{} // closed when process exits
}

// info returns a snapshot of the process state.
func (p *process) info() workspacesdk.ProcessInfo {
	p.mu.Lock()
	defer p.mu.Unlock()

	return workspacesdk.ProcessInfo{
		ID:          p.id,
		Command:     p.command,
		Argv:        append([]string(nil), p.argv...),
		WorkDir:     p.workDir,
		Host:        p.host,
		Port:        p.port,
		Tool:        p.tool,
		Background:  p.background,
		Interactive: p.interactive,
		Running:     p.running,
		ExitCode:    p.exitCode,
		StartedAt:   p.startedAt,
		ExitedAt:    p.exitedAt,
	}
}

// output returns the truncated output from the process buffer
// along with optional truncation metadata.
func (p *process) output() (string, *workspacesdk.ProcessTruncation) {
	return p.buf.Output()
}

// manager tracks processes spawned by the agent.
type manager struct {
	mu                    sync.Mutex
	launchMu              sync.Mutex
	logger                slog.Logger
	execer                agentexec.Execer
	fs                    afero.Fs
	clock                 quartz.Clock
	procs                 map[string]*process
	closed                bool
	updateEnv             func(current []string) (updated []string, err error)
	workingDir            func() string
	envInfo               usershell.EnvInfoer
	reportCommandActivity CommandActivityReporter
}

// newManager creates a new process manager.
func newManager(logger slog.Logger, execer agentexec.Execer, fs afero.Fs, envInfo usershell.EnvInfoer, updateEnv func(current []string) (updated []string, err error), workingDir func() string) *manager {
	if fs == nil {
		fs = afero.NewOsFs()
	}
	if envInfo == nil {
		envInfo = &usershell.SystemEnvInfo{}
	}
	return &manager{
		logger:     logger,
		execer:     execer,
		fs:         fs,
		clock:      quartz.NewReal(),
		procs:      make(map[string]*process),
		updateEnv:  updateEnv,
		workingDir: workingDir,
		envInfo:    envInfo,
	}
}

// startOrReuse serializes assistant-facing launches so duplicate detection and
// process creation are atomic with respect to other launches. Deduplication is
// scoped to a trusted invocation identity supplied by the server (a chat scope
// for chatd or an MCP-session scope for Remote MCP). Callers without such a
// scope retain the historical always-start behavior.
func (m *manager) startOrReuse(req workspacesdk.StartProcessRequest, chatID, dedupeScope string) (*process, bool, error) {
	m.launchMu.Lock()
	defer m.launchMu.Unlock()

	if dedupeScope != "" && req.Fingerprint != "" && !req.AllowDuplicate {
		m.mu.Lock()
		candidates := make([]*process, 0, len(m.procs))
		for _, proc := range m.procs {
			candidates = append(candidates, proc)
		}
		m.mu.Unlock()

		for _, proc := range candidates {
			proc.mu.Lock()
			match := proc.running && proc.dedupeScope == dedupeScope && proc.fingerprint == req.Fingerprint
			proc.mu.Unlock()
			if match {
				return proc, false, nil
			}
		}
	}

	proc, err := m.start(req, chatID, dedupeScope)
	if err != nil {
		return nil, false, err
	}
	return proc, true, nil
}

// start spawns a new process. Both foreground and background
// processes use a long-lived context so the process survives
// the HTTP request lifecycle. The background flag only affects
// client-side polling behavior.
func (m *manager) start(req workspacesdk.StartProcessRequest, chatID, dedupeScope string) (*process, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, xerrors.New("manager is closed")
	}
	m.mu.Unlock()

	id := uuid.New().String()
	logger := m.logger
	if chatID != "" {
		logger = logger.With(slog.F("chat_id", chatID))
	}

	// Use a cancellable context so Close() can terminate
	// all processes. context.Background() is the parent so
	// the process is not tied to any HTTP request.
	if (req.Command == "") == (len(req.Argv) == 0) {
		return nil, xerrors.New("exactly one of command or argv must be provided")
	}
	if len(req.Argv) > 0 && req.Argv[0] == "" {
		return nil, xerrors.New("argv[0] must not be empty")
	}

	if err := validateSSHProcessTarget(req); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	var cmd *exec.Cmd
	processWorkDir := req.WorkDir
	remotePIDFile := ""
	if req.Host != "" {
		var err error
		cmd, remotePIDFile, err = m.newRemoteProcessCommand(ctx, req, id)
		if err != nil {
			cancel()
			return nil, err
		}
	} else {
		if len(req.Argv) > 0 {
			cmd = m.execer.CommandContext(ctx, req.Argv[0], req.Argv[1:]...)
		} else {
			cmd = m.execer.CommandContext(ctx, "sh", "-c", req.Command)
		}
		cmd.Dir = m.resolveWorkingDirectory(req.WorkDir)
		processWorkDir = cmd.Dir
	}
	cmd.SysProcAttr = procSysProcAttr()

	var stdin io.WriteCloser
	switch {
	case req.Interactive:
		var err error
		stdin, err = cmd.StdinPipe()
		if err != nil {
			cancel()
			return nil, xerrors.Errorf("create stdin pipe: %w", err)
		}
	case req.Stdin != "":
		cmd.Stdin = strings.NewReader(req.Stdin)
	default:
		cmd.Stdin = nil
	}

	// WaitDelay ensures cmd.Wait returns promptly after
	// the process is killed, even if child processes are
	// still holding the stdout/stderr pipes open.
	cmd.WaitDelay = 5 * time.Second

	buf := NewHeadTailBuffer()
	activityOutput := &deferredActivityWriter{}
	combinedOutput := &processActivityWriter{process: buf, activity: activityOutput}
	cmd.Stdout = combinedOutput
	cmd.Stderr = combinedOutput

	// Build the process environment. If the manager has an
	// updateEnv hook (provided by the agent), use it to get the
	// full agent environment including GIT_ASKPASS, CODER_* vars,
	// etc. Otherwise fall back to the current process env.
	baseEnv := os.Environ()
	if m.updateEnv != nil {
		updated, err := m.updateEnv(baseEnv)
		if err != nil {
			logger.Warn(
				context.Background(),
				"failed to update command environment, falling back to os env",
				slog.Error(err),
			)
		} else {
			baseEnv = updated
		}
	}

	// Always set cmd.Env explicitly so that req.Env overrides
	// are applied on top of the full agent environment.
	cmd.Env = baseEnv
	// Remote environment overrides are encoded into the safely quoted remote
	// command. Do not also apply them to the local ssh client process.
	if req.Host == "" {
		for k, v := range req.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}
	// Propagate the chat ID so child processes (e.g.
	// GIT_ASKPASS) can send it back to the server.
	if chatID != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("CODER_CHAT_ID=%s", chatID))
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, xerrors.Errorf("start process: %w", err)
	}
	if req.Interactive && req.Stdin != "" {
		if _, err := io.WriteString(stdin, req.Stdin); err != nil {
			cancel()
			_ = cmd.Wait()
			return nil, xerrors.Errorf("write initial stdin: %w", err)
		}
	}

	now := m.clock.Now().Unix()
	tool := normalizeCommandActivityTool(req.Tool)
	source := normalizeCommandActivitySource(tool, chatID)
	proc := &process{
		id:            id,
		command:       req.Command,
		argv:          append([]string(nil), req.Argv...),
		workDir:       processWorkDir,
		host:          req.Host,
		port:          req.Port,
		identityFile:  req.IdentityFile,
		remotePIDFile: remotePIDFile,
		tool:          tool,
		fingerprint:   req.Fingerprint,
		background:    req.Background,
		interactive:   req.Interactive,
		chatID:        chatID,
		dedupeScope:   dedupeScope,
		cmd:           cmd,
		stdin:         stdin,
		cancel:        cancel,
		buf:           buf,
		logger:        logger,
		running:       true,
		startedAt:     now,
		done:          make(chan struct{}),
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		// Manager closed between our check and now. Kill the
		// process we just started.
		cancel()
		_ = cmd.Wait()
		return nil, xerrors.New("manager is closed")
	}
	m.procs[id] = proc
	m.mu.Unlock()

	activity := CommandActivity{
		Output: io.Discard,
		Finish: func(int) {},
	}
	if m.reportCommandActivity != nil {
		activity = m.reportCommandActivity(source, req.Command, req.Argv, req.Env, processWorkDir, tool)
		if activity.Output == nil {
			activity.Output = io.Discard
		}
		if activity.Finish == nil {
			activity.Finish = func(int) {}
		}
	}
	// cmd.Start can begin copying output before it returns. Attach the activity
	// sink only after the STARTED event has been created; the deferred writer
	// flushes any bytes produced in that tiny window in the correct order.
	activityOutput.setSink(activity.Output)

	go func() {
		err := cmd.Wait()
		exitedAt := m.clock.Now().Unix()

		proc.mu.Lock()
		proc.running = false
		proc.exitedAt = &exitedAt
		code := 0
		if err != nil {
			// Extract the exit code from the error.
			var exitErr *exec.ExitError
			if xerrors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			} else {
				// Unknown error; use -1 as a sentinel.
				code = -1
				proc.logger.Warn(
					context.Background(),
					"process wait returned non-exit error",
					slog.F("id", id),
					slog.Error(err),
				)
			}
		}
		proc.exitCode = &code
		proc.mu.Unlock()
		activity.Finish(code)
		_ = proc.closeInput()

		// Wake any waiters blocked on new output or
		// process exit before closing the done channel.
		proc.buf.Close()
		close(proc.done)
	}()

	return proc, nil
}

type processInputOptions struct {
	closeAfter bool
}

func (p *process) writeInput(data string, options processInputOptions) error {
	p.inputMu.Lock()
	defer p.inputMu.Unlock()
	if !p.interactive || p.stdin == nil {
		return xerrors.New("process stdin is not interactive")
	}
	if p.stdinClosed {
		return xerrors.New("process stdin is already closed")
	}
	if data != "" {
		if _, err := io.WriteString(p.stdin, data); err != nil {
			return xerrors.Errorf("write process stdin: %w", err)
		}
	}
	if options.closeAfter {
		if err := p.stdin.Close(); err != nil {
			return xerrors.Errorf("close process stdin: %w", err)
		}
		p.stdinClosed = true
	}
	return nil
}

func (p *process) closeInput() error {
	p.inputMu.Lock()
	defer p.inputMu.Unlock()
	if p.stdin == nil || p.stdinClosed {
		return nil
	}
	p.stdinClosed = true
	return p.stdin.Close()
}

// get returns a process by ID.
func (m *manager) get(id string) (*process, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	proc, ok := m.procs[id]
	return proc, ok
}

// list returns info about all tracked processes. Exited
// processes older than exitedProcessReapAge are removed.
// If chatID is non-empty, only processes belonging to that
// chat are returned.
func (m *manager) list(chatID string) []workspacesdk.ProcessInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock.Now()
	infos := make([]workspacesdk.ProcessInfo, 0, len(m.procs))
	for id, proc := range m.procs {
		info := proc.info()
		// Reap processes that exited more than 5 minutes ago
		// to prevent unbounded map growth.
		if !info.Running && info.ExitedAt != nil {
			exitedAt := time.Unix(*info.ExitedAt, 0)
			if now.Sub(exitedAt) > exitedProcessReapAge {
				delete(m.procs, id)
				continue
			}
		}
		// Filter by chatID if provided.
		if chatID != "" && proc.chatID != chatID {
			continue
		}
		infos = append(infos, info)
	}
	return infos
}

// input writes to an interactive process stdin.
func (m *manager) input(id string, data string, closeAfter bool) error {
	m.mu.Lock()
	proc, ok := m.procs[id]
	m.mu.Unlock()
	if !ok {
		return errProcessNotFound
	}
	proc.mu.Lock()
	running := proc.running
	proc.mu.Unlock()
	if !running {
		return errProcessNotRunning
	}
	return proc.writeInput(data, processInputOptions{closeAfter: closeAfter})
}

// signal sends a signal to a running process. It returns
// sentinel errors errProcessNotFound and errProcessNotRunning
// so callers can distinguish failure modes.
func (m *manager) signalContext(ctx context.Context, id string, sig string) error {
	m.mu.Lock()
	proc, ok := m.procs[id]
	m.mu.Unlock()
	if !ok {
		return errProcessNotFound
	}

	proc.mu.Lock()
	if !proc.running {
		proc.mu.Unlock()
		return errProcessNotRunning
	}
	host := proc.host
	port := proc.port
	identityFile := proc.identityFile
	pidFile := proc.remotePIDFile
	localProcess := proc.cmd.Process
	proc.mu.Unlock()

	if host != "" {
		if err := m.signalRemoteProcess(ctx, host, identityFile, port, pidFile, sig); err != nil {
			return xerrors.Errorf("signal remote process: %w", err)
		}
		return nil
	}

	var signal syscall.Signal
	switch sig {
	case "interrupt":
		signal = syscall.SIGINT
	case "terminate":
		signal = syscall.SIGTERM
	case "kill":
		signal = syscall.SIGKILL
	default:
		return xerrors.Errorf("unsupported signal %q", sig)
	}
	if err := signalProcess(localProcess, signal); err != nil {
		return xerrors.Errorf("signal process: %w", err)
	}
	return nil
}

// Close kills all running processes and prevents new ones from
// starting. It cancels each process's context, which causes
// CommandContext to kill the process and its pipe goroutines to
// drain.
func (m *manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	procs := make([]*process, 0, len(m.procs))
	for _, p := range m.procs {
		procs = append(procs, p)
	}
	m.mu.Unlock()

	for _, p := range procs {
		p.mu.Lock()
		remote := p.running && p.host != ""
		id := p.id
		p.mu.Unlock()
		if remote {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = m.signalContext(ctx, id, "kill")
			cancel()
		}
		_ = p.closeInput()
		p.cancel()
	}

	// Wait for all processes to exit.
	for _, p := range procs {
		<-p.done
	}

	return nil
}

// waitForOutput blocks until the buffer is closed (process
// exited) or the context is canceled. Returns nil when the
// buffer closed, ctx.Err() when the context expired.
func (p *process) waitForOutput(ctx context.Context) error {
	return p.waitForOutputSince(ctx, nil)
}

func (p *process) waitForOutputSince(ctx context.Context, cursor *int64) error {
	p.buf.cond.L.Lock()
	defer p.buf.cond.L.Unlock()

	startTotal := int64(p.buf.totalBytes)
	if cursor != nil {
		startTotal = *cursor
	}
	nevermind := make(chan struct{})
	defer close(nevermind)
	go func() {
		select {
		case <-ctx.Done():
			p.buf.cond.L.Lock()
			defer p.buf.cond.L.Unlock()
			p.buf.cond.Broadcast()
		case <-nevermind:
		}
	}()

	for ctx.Err() == nil && !p.buf.closed {
		if cursor != nil && int64(p.buf.totalBytes) > startTotal {
			return nil
		}
		p.buf.cond.Wait()
	}
	return ctx.Err()
}

// resolveWorkingDirectory returns the directory a process should start in.
// Priority: explicit request dir > agent configured dir > user home.
// The configured dir > home tail is shared with SSH sessions via
// usershell.ResolveWorkingDirectory so the two cannot drift.
func (m *manager) resolveWorkingDirectory(requested string) string {
	if requested != "" {
		return requested
	}
	var configured string
	if m.workingDir != nil {
		configured = m.workingDir()
	}
	dir, err := usershell.ResolveWorkingDirectory(m.fs, m.envInfo, configured)
	if err != nil {
		return ""
	}
	return dir
}
