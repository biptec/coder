package agentproc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
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
	switch tool = strings.TrimSpace(tool); tool {
	case "exec", "process_start", "bash":
		return tool
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

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func trackedRemoteCommand(command, pidFile string) string {
	inner := "printf '%s\\n' \"$$\" > " + shellQuote(pidFile) + "; exec sh -c " + shellQuote(command)
	runner := "sh -c " + shellQuote(inner)
	return "if setsid -w true >/dev/null 2>&1; then setsid -w " + runner + "; else " + runner + "; fi; __coder_status=$?; rm -f -- " + shellQuote(pidFile) + "; exit $__coder_status"
}

func buildSSHRemoteCommand(req workspacesdk.StartProcessRequest) (string, error) {
	host := strings.TrimSpace(req.Host)
	if host == "" {
		return "", xerrors.New("host cannot be empty")
	}
	if strings.ContainsAny(host, "\r\n\x00") {
		return "", xerrors.New("host cannot contain newline or NUL characters")
	}

	parts := make([]string, 0, 8+len(req.Argv)+len(req.Env))
	if req.WorkDir != "" {
		parts = append(parts, "cd", shellQuote(req.WorkDir), "&&")
	}
	parts = append(parts, "exec")
	if len(req.Env) > 0 {
		parts = append(parts, "env")
		keys := make([]string, 0, len(req.Env))
		for key := range req.Env {
			if !validEnvironmentName(key) {
				return "", xerrors.Errorf("invalid remote environment variable name %q", key)
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, shellQuote(key+"="+req.Env[key]))
		}
	}
	if len(req.Argv) > 0 {
		for _, arg := range req.Argv {
			parts = append(parts, shellQuote(arg))
		}
	} else {
		parts = append(parts, "sh", "-c", shellQuote(req.Command))
	}
	return strings.Join(parts, " "), nil
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
	identityFile  string
	remotePIDFile string
	tool          string
	background    bool
	interactive   bool
	chatID        string
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

// start spawns a new process. Both foreground and background
// processes use a long-lived context so the process survives
// the HTTP request lifecycle. The background flag only affects
// client-side polling behavior.
func (m *manager) start(req workspacesdk.StartProcessRequest, chatID string) (*process, error) {
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

	ctx, cancel := context.WithCancel(context.Background())
	var cmd *exec.Cmd
	processWorkDir := req.WorkDir
	remotePIDFile := ""
	if req.Host != "" {
		remoteCommand, err := buildSSHRemoteCommand(req)
		if err != nil {
			cancel()
			return nil, err
		}
		remotePIDFile = "/tmp/coder-mcp-process-" + id + ".pid"
		remoteCommand = trackedRemoteCommand(remoteCommand, remotePIDFile)
		sshArgs := []string{"-o", "BatchMode=yes"}
		if req.IdentityFile != "" {
			sshArgs = append(sshArgs, "-i", req.IdentityFile)
		}
		sshArgs = append(sshArgs, "--", req.Host, remoteCommand)
		cmd = m.execer.CommandContext(ctx, "ssh", sshArgs...)
		// workdir belongs to the remote command. The local ssh client starts from
		// the normal workspace directory so remote-only paths never break startup.
		cmd.Dir = m.resolveWorkingDirectory("")
	} else {
		if req.IdentityFile != "" {
			cancel()
			return nil, xerrors.New("identity_file requires host")
		}
		if len(req.Argv) > 0 {
			cmd = m.execer.CommandContext(ctx, req.Argv[0], req.Argv[1:]...)
		} else {
			cmd = m.execer.CommandContext(ctx, "sh", "-c", req.Command)
		}
		cmd.Dir = m.resolveWorkingDirectory(req.WorkDir)
		processWorkDir = cmd.Dir
	}
	cmd.SysProcAttr = procSysProcAttr()
	cmd.Cancel = func() error {
		return killProcessGroup(cmd.Process)
	}

	var stdin io.WriteCloser
	if req.Interactive {
		var err error
		stdin, err = cmd.StdinPipe()
		if err != nil {
			cancel()
			return nil, xerrors.Errorf("create stdin pipe: %w", err)
		}
	} else if req.Stdin != "" {
		cmd.Stdin = strings.NewReader(req.Stdin)
	} else {
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
		host:          strings.TrimSpace(req.Host),
		identityFile:  req.IdentityFile,
		remotePIDFile: remotePIDFile,
		tool:          tool,
		background:    req.Background,
		interactive:   req.Interactive,
		chatID:        chatID,
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

func (p *process) writeInput(data string, closeAfter bool) error {
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
	if closeAfter {
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
	return proc.writeInput(data, closeAfter)
}

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
	identityFile := proc.identityFile
	pidFile := proc.remotePIDFile
	localProcess := proc.cmd.Process
	proc.mu.Unlock()

	var signal syscall.Signal
	switch sig {
	case "kill":
		signal = syscall.SIGKILL
	case "terminate":
		signal = syscall.SIGTERM
	default:
		return xerrors.Errorf("unsupported signal %q", sig)
	}

	if host != "" {
		if err := m.signalRemoteProcess(ctx, host, identityFile, pidFile, signal); err != nil {
			return xerrors.Errorf("signal remote process: %w", err)
		}
		return nil
	}
	if err := signalProcess(localProcess, signal); err != nil {
		return xerrors.Errorf("signal process: %w", err)
	}
	return nil
}

func (m *manager) signalRemoteProcess(ctx context.Context, host, identityFile, pidFile string, signal syscall.Signal) error {
	if host == "" || pidFile == "" {
		return xerrors.New("remote process control metadata is incomplete")
	}
	signalName := "TERM"
	if signal == syscall.SIGKILL {
		signalName = "KILL"
	}
	remoteCommand := "i=0; while [ ! -s " + shellQuote(pidFile) + " ] && [ \"$i\" -lt 30 ]; do i=$((i+1)); sleep 0.1; done; " +
		"if [ ! -s " + shellQuote(pidFile) + " ]; then echo 'remote process pid is not available' >&2; exit 3; fi; " +
		"pid=$(cat -- " + shellQuote(pidFile) + "); case \"$pid\" in ''|*[!0-9]*) echo 'invalid remote process pid' >&2; exit 4;; esac; " +
		"if ! kill -0 \"$pid\" 2>/dev/null; then exit 0; fi; " +
		"kill -" + signalName + " -- \"-$pid\" 2>/dev/null || kill -" + signalName + " -- \"$pid\""
	args := []string{"-o", "BatchMode=yes"}
	if identityFile != "" {
		args = append(args, "-i", identityFile)
	}
	args = append(args, "--", host, remoteCommand)
	cmd := m.execer.CommandContext(ctx, "ssh", args...)
	cmd.Dir = m.resolveWorkingDirectory("")
	baseEnv := os.Environ()
	if m.updateEnv != nil {
		if updated, err := m.updateEnv(baseEnv); err == nil {
			baseEnv = updated
		}
	}
	cmd.Env = baseEnv
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return xerrors.Errorf("remote signal command failed: %s", detail)
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
