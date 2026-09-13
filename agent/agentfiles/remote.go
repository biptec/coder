package agentfiles

import (
	"context"
	"os/exec"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/agent/agentexec"
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
	if execer == nil {
		execer = agentexec.DefaultExecer
	}
	args := []string{"-o", "BatchMode=yes"}
	if identityFile != "" {
		args = append(args, "-i", identityFile)
	}
	args = append(args, "--", host, command)
	return execer.CommandContext(ctx, "ssh", args...), nil
}
