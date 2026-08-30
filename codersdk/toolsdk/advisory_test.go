package toolsdk

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func TestCommandInvokesSudo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{
			name:    "direct",
			command: `sudo apt-get update`,
			want:    true,
		},
		{
			name:    "compound pipeline",
			command: `printf before && cd /tmp; (printf data | /usr/bin/sudo tee /tmp/value)`,
			want:    true,
		},
		{
			name:    "command substitution",
			command: `value="$(sudo -n id)"; printf '%s\n' "$value"`,
			want:    true,
		},
		{
			name:    "quoted command word",
			command: `'sudo' -n true`,
			want:    true,
		},
		{
			name:    "argument only",
			command: `printf '%s\n' sudo`,
			want:    false,
		},
		{
			name:    "quoted data",
			command: `printf '%s\n' "sudo apt-get install chromium"`,
			want:    false,
		},
		{
			name:    "comment only",
			command: "printf ok # sudo apt-get update\n",
			want:    false,
		},
		{
			name:    "substring",
			command: `printf pseudosudo`,
			want:    false,
		},
		{
			name:    "dynamic command cannot be resolved statically",
			command: `$SUDO apt-get update`,
			want:    false,
		},
		{
			name:    "partial parse keeps earlier sudo invocation",
			command: `printf before; sudo -n true; "`,
			want:    true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, commandInvokesSudo(tt.command))
		})
	}
}

func TestCommandAdvisories(t *testing.T) {
	t.Parallel()

	require.Empty(t, commandAdvisories(`printf ok`))

	advisories := commandAdvisories(`printf before && sudo -n true`)
	require.Len(t, advisories, 1)
	require.Equal(t, sudoEphemeralFilesystemAdvisoryCode, advisories[0].Code)
	require.Contains(t, advisories[0].Message, "/home/coder")
	require.Contains(t, advisories[0].Message, "$HOME")
}

func TestAdvisoryIsSeparateFromCommandOutput(t *testing.T) {
	t.Parallel()

	result := WorkspaceBashResult{
		Output:     `{"status":"ok"}`,
		ExitCode:   0,
		Advisories: commandAdvisories(`sudo -n true`),
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"output":"{\"status\":\"ok\"}",
		"exit_code":0,
		"advisories":[{
			"code":"SUDO_EPHEMERAL_ROOTFS",
			"message":"This workspace's system filesystem is ephemeral. In the standard Developer Workspace, only /home/coder is persistent across workspace recreation. Treat changes made with sudo outside /home/coder as temporary; they will be lost when the workspace is recreated. Prefer durable tools and dependencies under $HOME, and use sudo for system changes only when they are intentionally temporary."
		}]
	}`, string(data))
	require.Equal(t, `{"status":"ok"}`, result.Output)
}

func TestProcessListAdvisoryPreservesFlatJSONShape(t *testing.T) {
	t.Parallel()

	result := WorkspaceProcessListResult{Processes: []WorkspaceProcessInfo{{
		ProcessInfo: workspacesdk.ProcessInfo{
			ID:      "proc-1",
			Command: "printf before && sudo -n true",
			Running: true,
		},
		Advisories: commandAdvisories(`printf before && sudo -n true`),
	}}}

	data, err := json.Marshal(result)
	require.NoError(t, err)
	require.Contains(t, string(data), `"id":"proc-1"`)
	require.Contains(t, string(data), `"command":"printf before `)
	require.Contains(t, string(data), `sudo -n true"`)
	require.Contains(t, string(data), `"running":true`)
	require.Contains(t, string(data), `"advisories":[`)
	require.NotContains(t, string(data), `"ProcessInfo"`)
}
