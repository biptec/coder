package agentproc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/agent/sshconfig"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	maxRunCommandInputBytes  = 4 << 20
	maxRunCommandOutputBytes = 8 << 20
	maxRunCommandStderrBytes = 64 << 10
)

type boundedRunBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *boundedRunBuffer) Write(p []byte) (int, error) {
	original := len(p)
	if b.max <= 0 {
		b.truncated = b.truncated || original > 0
		return original, nil
	}
	remaining := b.max - b.buf.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || original > 0
		return original, nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return original, nil
	}
	_, _ = b.buf.Write(p)
	return original, nil
}

func (api *API) handleRunCommand(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req workspacesdk.RunCommandRequest
	if err := jsonDecodeRunRequest(r, &req); err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: err.Error()})
		return
	}
	resp, err := api.runCommand(ctx, req)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: err.Error()})
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, resp)
}

func jsonDecodeRunRequest(r *http.Request, req *workspacesdk.RunCommandRequest) error {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(req); err != nil {
		return xerrors.Errorf("request body must be valid JSON: %w", err)
	}
	return nil
}

func (api *API) runCommand(ctx context.Context, req workspacesdk.RunCommandRequest) (workspacesdk.RunCommandResponse, error) {
	if (req.Command == "") == (len(req.Argv) == 0) {
		return workspacesdk.RunCommandResponse{}, xerrors.New("exactly one of command or argv is required")
	}
	if len(req.Argv) > 0 && req.Argv[0] == "" {
		return workspacesdk.RunCommandResponse{}, xerrors.New("argv[0] must not be empty")
	}
	stdin, err := base64.StdEncoding.DecodeString(req.StdinBase64)
	if err != nil {
		return workspacesdk.RunCommandResponse{}, xerrors.Errorf("decode stdin_base64: %w", err)
	}
	if len(stdin) > maxRunCommandInputBytes {
		return workspacesdk.RunCommandResponse{}, xerrors.Errorf("stdin cannot exceed %d bytes", maxRunCommandInputBytes)
	}

	var cmd *exec.Cmd
	if req.Host != "" {
		if err := sshconfig.ValidateAlias(req.Host); err != nil {
			return workspacesdk.RunCommandResponse{}, err
		}
		remoteCommand, err := buildSSHRemoteCommand(workspacesdk.StartProcessRequest{
			Command:      req.Command,
			Argv:         req.Argv,
			WorkDir:      req.WorkDir,
			Env:          req.Env,
			Host:         req.Host,
			IdentityFile: req.IdentityFile,
		})
		if err != nil {
			return workspacesdk.RunCommandResponse{}, err
		}
		args := []string{"-o", "BatchMode=yes"}
		if req.IdentityFile != "" {
			if !filepath.IsAbs(req.IdentityFile) {
				return workspacesdk.RunCommandResponse{}, xerrors.New("identity_file must be an absolute workspace path")
			}
			args = append(args, "-i", req.IdentityFile)
		}
		args = append(args, "--", strings.TrimSpace(req.Host), remoteCommand)
		cmd = api.manager.execer.CommandContext(ctx, "ssh", args...)
		cmd.Dir = api.manager.resolveWorkingDirectory("")
	} else {
		if req.IdentityFile != "" {
			return workspacesdk.RunCommandResponse{}, xerrors.New("identity_file requires host")
		}
		if len(req.Argv) > 0 {
			cmd = api.manager.execer.CommandContext(ctx, req.Argv[0], req.Argv[1:]...)
		} else {
			cmd = api.manager.execer.CommandContext(ctx, "sh", "-c", req.Command)
		}
		cmd.Dir = api.manager.resolveWorkingDirectory(req.WorkDir)
	}
	cmd.SysProcAttr = procSysProcAttr()
	cmd.Cancel = func() error {
		return killProcessGroup(cmd.Process)
	}
	cmd.WaitDelay = 5 * time.Second
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	baseEnv := os.Environ()
	if api.manager.updateEnv != nil {
		if updated, updateErr := api.manager.updateEnv(baseEnv); updateErr == nil {
			baseEnv = updated
		}
	}
	cmd.Env = baseEnv
	if req.Host == "" {
		for key, value := range req.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}
	}

	stdout := &boundedRunBuffer{max: maxRunCommandOutputBytes}
	stderr := &boundedRunBuffer{max: maxRunCommandStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	if ctx.Err() != nil {
		return workspacesdk.RunCommandResponse{}, ctx.Err()
	}
	if stdout.truncated {
		return workspacesdk.RunCommandResponse{}, xerrors.Errorf("command output exceeded %d bytes", maxRunCommandOutputBytes)
	}

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return workspacesdk.RunCommandResponse{}, xerrors.Errorf("run command: %w", runErr)
		}
		exitCode = exitErr.ExitCode()
	}
	stderrText := strings.ToValidUTF8(stderr.buf.String(), "�")
	if stderr.truncated {
		stderrText += "\n[stderr truncated]"
	}
	return workspacesdk.RunCommandResponse{
		StdoutBase64: base64.StdEncoding.EncodeToString(stdout.buf.Bytes()),
		Stderr:       stderrText,
		ExitCode:     exitCode,
	}, nil
}
