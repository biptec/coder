package sshconfig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/agent/sshconfig"
)

func TestListFromFile(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	require.NoError(t, os.MkdirAll(filepath.Join(sshDir, "conf.d"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sshDir, "config"), []byte(`
Host coder1 coder-2
  HostName 10.0.0.1
Host=equal_style
Host = spaced_equal
Host "quoted_alias" coder1 # comment
Host *.example.com !blocked.example.com
Include=conf.d/*.conf
Host user@unsafe
Host "unterminated
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(sshDir, "conf.d", "extra.conf"), []byte(`
Host build_host
Host coder1
`), 0o600))

	hosts, err := sshconfig.ListFromFile(filepath.Join(sshDir, "config"), home)
	require.NoError(t, err)
	require.Equal(t, []sshconfig.Host{{Alias: "build_host"}, {Alias: "coder-2"}, {Alias: "coder1"}, {Alias: "equal_style"}, {Alias: "quoted_alias"}, {Alias: "spaced_equal"}}, hosts)
}

func TestListFromFileMissing(t *testing.T) {
	t.Parallel()

	hosts, err := sshconfig.ListFromFile(filepath.Join(t.TempDir(), "missing"), t.TempDir())
	require.NoError(t, err)
	require.Empty(t, hosts)
}
