package agentfiles

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/agent/agentexec"
	"github.com/coder/coder/v2/agent/sshconfig"
)

func remoteShellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func remoteSSHCommand(ctx context.Context, execer agentexec.Execer, host, identityFile, command string) (*exec.Cmd, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, xerrors.New("host cannot be empty")
	}
	if err := sshconfig.ValidateAlias(host); err != nil {
		return nil, err
	}
	if execer == nil {
		execer = agentexec.DefaultExecer
	}
	args := []string{"-o", "BatchMode=yes"}
	if identityFile != "" {
		if !filepath.IsAbs(identityFile) {
			return nil, xerrors.New("identity_file must be an absolute workspace path")
		}
		args = append(args, "-i", identityFile)
	}
	args = append(args, "--", host, command)
	return execer.CommandContext(ctx, "ssh", args...), nil
}
