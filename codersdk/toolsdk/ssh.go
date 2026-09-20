package toolsdk

import (
	"path/filepath"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// WorkspaceSSHOptions routes command execution through the workspace's OpenSSH
// client. The private-key path is intentionally input-only; process metadata
// exposes the target host and port, but never the identity file.
type WorkspaceSSHOptions struct {
	Host         string `json:"host"`
	IdentityFile string `json:"identity_file,omitempty"`
	Port         int    `json:"port,omitempty"`
}

func workspaceSSHSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          "Optional remote execution target reached from inside the workspace via OpenSSH. Omit ssh to execute locally in the workspace.",
		"additionalProperties": false,
		"properties": map[string]any{
			"host": map[string]any{
				"type":        "string",
				"description": "SSH destination host or user@host. OpenSSH config aliases are allowed. The value is passed as one argv element and is never interpreted as shell text.",
				"minLength":   1,
			},
			"identity_file": map[string]any{
				"type":        "string",
				"description": "Optional absolute path inside the workspace to an SSH private key. The path is not returned in process/session metadata.",
				"minLength":   1,
			},
			"port": map[string]any{
				"type":        "integer",
				"description": "Optional SSH destination port. Omit it to use OpenSSH configuration or the default port.",
				"minimum":     1,
				"maximum":     65535,
			},
		},
		"required": []string{"host"},
	}
}

func validateWorkspaceSSHOptions(ssh *WorkspaceSSHOptions) error {
	if ssh == nil {
		return nil
	}

	host := strings.TrimSpace(ssh.Host)
	if host == "" || host != ssh.Host {
		return xerrors.New("ssh.host cannot be empty or contain surrounding whitespace")
	}
	if strings.HasPrefix(host, "-") {
		return xerrors.New("ssh.host cannot begin with '-'")
	}
	if strings.ContainsAny(host, " \t\r\n\x00") {
		return xerrors.New("ssh.host cannot contain whitespace or NUL characters")
	}
	if ssh.IdentityFile != "" && !filepath.IsAbs(ssh.IdentityFile) {
		return xerrors.New("ssh.identity_file must be an absolute workspace path")
	}
	if ssh.Port < 0 || ssh.Port > 65535 {
		return xerrors.New("ssh.port must be between 1 and 65535")
	}
	return nil
}

func applyWorkspaceSSHOptions(req *workspacesdk.StartProcessRequest, ssh *WorkspaceSSHOptions) error {
	if err := validateWorkspaceSSHOptions(ssh); err != nil {
		return err
	}
	if ssh == nil {
		return nil
	}
	req.Host = ssh.Host
	req.IdentityFile = ssh.IdentityFile
	req.Port = ssh.Port
	return nil
}
