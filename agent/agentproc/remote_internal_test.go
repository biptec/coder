package agentproc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func TestBuildSSHRemoteCommandQuotesInputs(t *testing.T) {
	t.Parallel()

	command, err := buildSSHRemoteCommand(workspacesdk.StartProcessRequest{
		Host:    "user@example.com",
		WorkDir: "/tmp/work dir",
		Env: map[string]string{
			"SAFE": "a'b c",
		},
		Argv: []string{"printf", "%s", "hello; $(touch /tmp/nope)"},
	})
	require.NoError(t, err)
	require.Contains(t, command, "cd '/tmp/work dir' && exec env")
	require.Contains(t, command, shellQuote("SAFE=a'b c"))
	require.Contains(t, command, shellQuote("hello; $(touch /tmp/nope)"))
	require.NotContains(t, command, "identity_file")
}

func TestBuildSSHRemoteCommandRejectsInvalidEnvironmentName(t *testing.T) {
	t.Parallel()

	_, err := buildSSHRemoteCommand(workspacesdk.StartProcessRequest{
		Host: "example.com",
		Env:  map[string]string{"BAD-NAME": "value"},
		Argv: []string{"true"},
	})
	require.ErrorContains(t, err, "invalid remote environment variable name")
}

func TestTrackedRemoteCommandUsesPrivatePIDFile(t *testing.T) {
	t.Parallel()

	command := trackedRemoteCommand("exec 'sleep' '600'", "/tmp/coder-mcp-process-test.pid")
	require.Contains(t, command, "setsid")
	require.Contains(t, command, "/tmp/coder-mcp-process-test.pid")
	require.Contains(t, command, "$$")
	require.Contains(t, command, "rm -f")
}

func TestProcessInfoExposesHostButNotIdentityFile(t *testing.T) {
	t.Parallel()

	p := &process{
		id:           "process-1",
		command:      "uptime",
		host:         "admin@example.com",
		identityFile: "/home/coder/.ssh/private-key",
	}
	info := p.info()
	require.Equal(t, "admin@example.com", info.Host)

	data, err := json.Marshal(info)
	require.NoError(t, err)
	text := string(data)
	require.Contains(t, text, `"host":"admin@example.com"`)
	require.False(t, strings.Contains(text, "private-key"), text)
	require.NotContains(t, text, "identity_file")
}
