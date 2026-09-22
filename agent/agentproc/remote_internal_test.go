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

func TestBuildSSHRemoteCommandUsesExplicitPOSIXShell(t *testing.T) {
	t.Parallel()

	command, err := buildSSHRemoteCommand(workspacesdk.StartProcessRequest{
		Host:    "example.com",
		Command: "printf '%s\\n' \"$HOME\"",
	})
	require.NoError(t, err)
	require.Contains(t, command, "exec sh -c ")
	require.Contains(t, command, shellQuote("printf '%s\\n' \"$HOME\""))
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

func TestValidateSSHProcessTarget(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		req  workspacesdk.StartProcessRequest
		err  string
	}{
		{name: "IdentityWithoutHost", req: workspacesdk.StartProcessRequest{IdentityFile: "/tmp/key"}, err: "requires ssh.host"},
		{name: "PortWithoutHost", req: workspacesdk.StartProcessRequest{Port: 22}, err: "requires ssh.host"},
		{name: "RelativeIdentity", req: workspacesdk.StartProcessRequest{Host: "example.com", IdentityFile: ".ssh/key"}, err: "absolute"},
		{name: "InvalidPortNegative", req: workspacesdk.StartProcessRequest{Host: "example.com", Port: -1}, err: "between 1 and 65535"},
		{name: "InvalidPortHigh", req: workspacesdk.StartProcessRequest{Host: "example.com", Port: 65536}, err: "between 1 and 65535"},
		{name: "LeadingOption", req: workspacesdk.StartProcessRequest{Host: "-ProxyCommand=bad"}, err: "cannot begin"},
		{name: "Whitespace", req: workspacesdk.StartProcessRequest{Host: "user@bad host"}, err: "whitespace"},
		{name: "Newline", req: workspacesdk.StartProcessRequest{Host: "host\nother"}, err: "whitespace"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateSSHProcessTarget(tc.req)
			require.ErrorContains(t, err, tc.err)
		})
	}

	require.NoError(t, validateSSHProcessTarget(workspacesdk.StartProcessRequest{
		Host:         "user@example.com",
		IdentityFile: "/home/coder/.ssh/id_ed25519",
		Port:         2222,
	}))
	require.NoError(t, validateSSHProcessTarget(workspacesdk.StartProcessRequest{}))
}

func TestSSHClientArgsUseStructuredOptions(t *testing.T) {
	t.Parallel()

	args, err := sshClientArgs(workspacesdk.StartProcessRequest{
		Host:         "user@example.com",
		IdentityFile: "/home/coder/.ssh/key",
		Port:         2222,
	}, "echo ok")
	require.NoError(t, err)
	require.Equal(t, []string{
		"-o", "BatchMode=yes",
		"-i", "/home/coder/.ssh/key",
		"-p", "2222",
		"--", "user@example.com", "echo ok",
	}, args)
}

func TestTrackedRemoteCommandAvoidsZshStatusVariable(t *testing.T) {
	t.Parallel()

	command := trackedRemoteCommand("exec 'sleep' '600'", "/tmp/coder-mcp-process-test.pid")
	require.Contains(t, command, "setsid")
	require.Contains(t, command, "/tmp/coder-mcp-process-test.pid")
	require.Contains(t, command, "$$")
	require.Contains(t, command, "_coder_exit=$?")
	require.NotContains(t, command, "status=$?", "zsh exposes status as a read-only variable")
}

func TestProcessInfoExposesRemoteTargetButNotIdentityFile(t *testing.T) {
	t.Parallel()

	p := &process{
		id:           "process-1",
		command:      "uptime",
		host:         "admin@example.com",
		port:         2222,
		identityFile: "/home/coder/.ssh/private-key",
	}
	info := p.info()
	require.Equal(t, "admin@example.com", info.Host)
	require.Equal(t, 2222, info.Port)

	data, err := json.Marshal(info)
	require.NoError(t, err)
	text := string(data)
	require.Contains(t, text, "\"host\":\"admin@example.com\"")
	require.Contains(t, text, "\"port\":2222")
	require.False(t, strings.Contains(text, "private-key"), text)
	require.NotContains(t, text, "identity_file")
}

func TestNormalizeCommandActivityToolCanonicalNames(t *testing.T) {
	t.Parallel()

	require.Equal(t, "exec", normalizeCommandActivityTool("exec"))
	require.Equal(t, "execute_shell_command", normalizeCommandActivityTool("bash"))
	require.Equal(t, "execute_shell_command", normalizeCommandActivityTool("execute_shell_command"))
	require.Equal(t, "start_process", normalizeCommandActivityTool("process_start"))
	require.Equal(t, "start_process", normalizeCommandActivityTool("start_process"))
	require.Empty(t, normalizeCommandActivityTool("untrusted"))
}
