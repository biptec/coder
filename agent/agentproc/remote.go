package agentproc

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

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
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			r == '_' ||
			(i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func validateSSHProcessTarget(req workspacesdk.StartProcessRequest) error {
	host := strings.TrimSpace(req.Host)
	if req.Host == "" {
		if req.IdentityFile != "" {
			return xerrors.New("identity_file requires ssh.host")
		}
		if req.Port != 0 {
			return xerrors.New("ssh.port requires ssh.host")
		}
		return nil
	}
	if host == "" || host != req.Host {
		return xerrors.New("ssh host cannot be empty or contain surrounding whitespace")
	}
	if strings.HasPrefix(host, "-") {
		return xerrors.New("ssh host cannot begin with '-'")
	}
	if strings.ContainsAny(host, " \t\r\n\x00") {
		return xerrors.New("ssh host cannot contain whitespace or NUL characters")
	}
	if req.IdentityFile != "" && !filepath.IsAbs(req.IdentityFile) {
		return xerrors.New("identity_file must be an absolute workspace path")
	}
	if req.Port < 0 || req.Port > 65535 {
		return xerrors.New("ssh port must be between 1 and 65535")
	}
	return nil
}

func buildSSHRemoteCommand(req workspacesdk.StartProcessRequest) (string, error) {
	if err := validateSSHProcessTarget(req); err != nil {
		return "", err
	}
	if req.Host == "" {
		return "", xerrors.New("ssh host cannot be empty")
	}
	if (req.Command == "") == (len(req.Argv) == 0) {
		return "", xerrors.New("exactly one of command or argv must be provided")
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
		// Use POSIX sh explicitly so execute_shell_command has stable semantics
		// independent of the account's login shell (zsh, bash, etc.).
		parts = append(parts, "sh", "-c", shellQuote(req.Command))
	}
	return strings.Join(parts, " "), nil
}

// trackedRemoteCommand records the remote process-group leader in an
// unpredictable per-session file. The variable name intentionally avoids
// "status", which is read-only in zsh and caused the v2.35.3.27 regression.
func trackedRemoteCommand(command, pidFile string) string {
	inner := "umask 077; printf '%s\\n' \"$$\" > " + shellQuote(pidFile) +
		"; exec sh -c " + shellQuote(command)
	runner := "sh -c " + shellQuote(inner)
	return "if command -v setsid >/dev/null 2>&1 && setsid -w true >/dev/null 2>&1; " +
		"then setsid -w " + runner + "; else " + runner + "; fi; " +
		"_coder_exit=$?; rm -f -- " + shellQuote(pidFile) + "; exit \"$_coder_exit\""
}

func sshClientArgs(req workspacesdk.StartProcessRequest, remoteCommand string) ([]string, error) {
	if err := validateSSHProcessTarget(req); err != nil {
		return nil, err
	}
	args := []string{"-o", "BatchMode=yes"}
	if req.IdentityFile != "" {
		args = append(args, "-i", req.IdentityFile)
	}
	if req.Port != 0 {
		args = append(args, "-p", strconv.Itoa(req.Port))
	}
	args = append(args, "--", req.Host, remoteCommand)
	return args, nil
}

func (m *manager) newRemoteProcessCommand(
	ctx context.Context,
	req workspacesdk.StartProcessRequest,
	id string,
) (*exec.Cmd, string, error) {
	remoteCommand, err := buildSSHRemoteCommand(req)
	if err != nil {
		return nil, "", err
	}
	pidFile := "/tmp/coder-mcp-process-" + id + ".pid"
	remoteCommand = trackedRemoteCommand(remoteCommand, pidFile)
	args, err := sshClientArgs(req, remoteCommand)
	if err != nil {
		return nil, "", err
	}
	cmd := m.execer.CommandContext(ctx, "ssh", args...)
	// workdir belongs to the remote command. Never use a remote-only path as
	// the local ssh client's cwd.
	cmd.Dir = m.resolveWorkingDirectory("")
	return cmd, pidFile, nil
}

func (m *manager) signalRemoteProcess(
	ctx context.Context,
	host string,
	identityFile string,
	port int,
	pidFile string,
	signal string,
) error {
	if host == "" || pidFile == "" {
		return xerrors.New("remote process control metadata is incomplete")
	}

	var signalName string
	switch signal {
	case "interrupt":
		signalName = "INT"
	case "terminate":
		signalName = "TERM"
	case "kill":
		signalName = "KILL"
	default:
		return xerrors.Errorf("unsupported signal %q", signal)
	}

	remoteCommand := "i=0; while [ ! -s " + shellQuote(pidFile) +
		" ] && [ \"$i\" -lt 30 ]; do i=$((i+1)); sleep 0.1; done; " +
		"if [ ! -s " + shellQuote(pidFile) +
		" ]; then echo 'remote process pid is not available' >&2; exit 3; fi; " +
		"pid=$(cat -- " + shellQuote(pidFile) +
		"); case \"$pid\" in ''|*[!0-9]*) echo 'invalid remote process pid' >&2; exit 4;; esac; " +
		"if ! kill -0 \"$pid\" 2>/dev/null; then exit 0; fi; " +
		"kill -" + signalName + " -- \"-$pid\" 2>/dev/null || kill -" + signalName + " -- \"$pid\""

	req := workspacesdk.StartProcessRequest{
		Host:         host,
		IdentityFile: identityFile,
		Port:         port,
	}
	args, err := sshClientArgs(req, remoteCommand)
	if err != nil {
		return err
	}
	cmd := m.execer.CommandContext(ctx, "ssh", args...)
	cmd.Dir = m.resolveWorkingDirectory("")

	baseEnv := os.Environ()
	if m.updateEnv != nil {
		if updated, updateErr := m.updateEnv(baseEnv); updateErr == nil {
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
