package toolsdk

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

func TestWorkspaceSSHOptionsSchema(t *testing.T) {
	t.Parallel()

	schema := workspaceSSHSchema()
	require.Equal(t, "object", schema["type"])
	require.Equal(t, false, schema["additionalProperties"])
	require.Equal(t, []string{"host"}, schema["required"])

	properties := schema["properties"].(map[string]any)
	require.Contains(t, properties, "host")
	require.Contains(t, properties, "identity_file")
	require.Contains(t, properties, "port")

	port := properties["port"].(map[string]any)
	require.EqualValues(t, 1, port["minimum"])
	require.EqualValues(t, 65535, port["maximum"])
}

func TestWorkspaceSSHOptionsValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ssh  *WorkspaceSSHOptions
		err  string
	}{
		{name: "Nil"},
		{name: "Host", ssh: &WorkspaceSSHOptions{Host: "server-alias"}},
		{name: "UserHostPort", ssh: &WorkspaceSSHOptions{Host: "root@example.com", Port: 2222, IdentityFile: "/home/coder/.ssh/id_ed25519"}},
		{name: "EmptyHost", ssh: &WorkspaceSSHOptions{}, err: "ssh.host"},
		{name: "WhitespaceHost", ssh: &WorkspaceSSHOptions{Host: "host name"}, err: "whitespace"},
		{name: "SurroundingWhitespace", ssh: &WorkspaceSSHOptions{Host: " host"}, err: "surrounding whitespace"},
		{name: "OptionInjection", ssh: &WorkspaceSSHOptions{Host: "-oProxyCommand=bad"}, err: "cannot begin"},
		{name: "RelativeIdentity", ssh: &WorkspaceSSHOptions{Host: "host", IdentityFile: ".ssh/key"}, err: "absolute"},
		{name: "NegativePort", ssh: &WorkspaceSSHOptions{Host: "host", Port: -1}, err: "between 1 and 65535"},
		{name: "HighPort", ssh: &WorkspaceSSHOptions{Host: "host", Port: 65536}, err: "between 1 and 65535"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateWorkspaceSSHOptions(tc.ssh)
			if tc.err == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.err)
		})
	}
}

func TestApplyWorkspaceSSHOptions(t *testing.T) {
	t.Parallel()

	req := workspacesdk.StartProcessRequest{Argv: []string{"uptime"}}
	err := applyWorkspaceSSHOptions(&req, &WorkspaceSSHOptions{
		Host:         "admin@example.com",
		IdentityFile: "/home/coder/.ssh/key",
		Port:         2202,
	})
	require.NoError(t, err)
	require.Equal(t, "admin@example.com", req.Host)
	require.Equal(t, "/home/coder/.ssh/key", req.IdentityFile)
	require.Equal(t, 2202, req.Port)

	local := workspacesdk.StartProcessRequest{Argv: []string{"uptime"}}
	require.NoError(t, applyWorkspaceSSHOptions(&local, nil))
	require.Empty(t, local.Host)
	require.Empty(t, local.IdentityFile)
	require.Zero(t, local.Port)
}

func TestRemoteExecutionToolsExposeSSHAndOpenWorld(t *testing.T) {
	t.Parallel()

	tools := []struct {
		name        string
		properties  map[string]any
		annotations MCPToolAnnotations
		description string
	}{
		{name: "exec", properties: WorkspaceExec.Schema.Properties, annotations: WorkspaceExec.MCPAnnotations, description: WorkspaceExec.Description},
		{name: "execute_shell_command", properties: WorkspaceBash.Schema.Properties, annotations: WorkspaceBash.MCPAnnotations, description: WorkspaceBash.Description},
		{name: "start_process", properties: WorkspaceProcessStartV2.Schema.Properties, annotations: WorkspaceProcessStartV2.MCPAnnotations, description: WorkspaceProcessStartV2.Description},
	}
	for _, tool := range tools {
		tool := tool
		t.Run(tool.name, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, tool.properties, "ssh")
			sshSchema := tool.properties["ssh"].(map[string]any)
			require.Equal(t, []string{"host"}, sshSchema["required"])
			require.True(t, tool.annotations.DestructiveHint)
			require.True(t, tool.annotations.OpenWorldHint)
			require.Contains(t, tool.description, "OpenSSH")
		})
	}

	require.True(t, WorkspaceProcessInput.MCPAnnotations.OpenWorldHint)
	require.True(t, WorkspaceProcessSignal.MCPAnnotations.OpenWorldHint)
	require.False(t, WorkspaceProcessOutput.MCPAnnotations.OpenWorldHint)
	require.False(t, WorkspaceProcessList.MCPAnnotations.OpenWorldHint)
}
