package toolsdk

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodeQueryCommand(t *testing.T) {
	t.Parallel()

	argv, workdir, err := codeQueryCommand(WorkspaceCodeQueryArgs{
		Operation: "definition",
		Path:      "/repo/main.go",
		Line:      3,
		Column:    7,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"gopls", "definition", "/repo/main.go:3:7"}, argv)
	require.Equal(t, "/repo", workdir)

	argv, workdir, err = codeQueryCommand(WorkspaceCodeQueryArgs{
		Operation: "workspace_symbols",
		Root:      "/repo",
		Query:     "Handler",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"gopls", "workspace_symbol", "Handler"}, argv)
	require.Equal(t, "/repo", workdir)

	_, _, err = codeQueryCommand(WorkspaceCodeQueryArgs{
		Operation: "references",
		Path:      "/repo/main.go",
	})
	require.ErrorContains(t, err, "line and column")
}

func TestParseHTTPFetchOutput(t *testing.T) {
	t.Parallel()

	meta := "200\nhttps://example.com/final\n"
	headers := "HTTP/1.1 302 Found\r\nLocation: /final\r\n\r\nHTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nX-Test: one\r\nX-Test: two\r\n\r\n"
	body := "hello"
	payload := fmt.Sprintf("CODER_HTTP_FETCH_V1\n%d\n%d\n%d\n%s%s%s", len(meta), len(headers), len(body), meta, headers, body)

	result, err := parseHTTPFetchOutput([]byte(payload), 1024)
	require.NoError(t, err)
	require.Equal(t, 200, result.Status)
	require.Equal(t, "https://example.com/final", result.EffectiveURL)
	require.Equal(t, "text", result.Encoding)
	require.Equal(t, "hello", result.Body)
	require.Equal(t, []string{"text/plain"}, result.Headers["content-type"])
	require.Equal(t, []string{"one", "two"}, result.Headers["x-test"])
}

func TestBuildHTTPRequestCommand(t *testing.T) {
	t.Parallel()

	command := buildHTTPRequestCommand("https://example.com/api", "POST", map[string]string{
		"Content-Type": "application/json",
	}, 1024, true, true)
	require.Contains(t, command, "'--request' 'POST'")
	require.Contains(t, command, "'--data-binary' '@-'")
	require.Contains(t, command, "'Content-Type: application/json'")
	require.Contains(t, command, "'=http,https'")
	require.Contains(t, command, "'=http,https'")
	require.Contains(t, command, "'--location'")
}

func TestValidateRemoteTarget(t *testing.T) {
	t.Parallel()

	require.NoError(t, validateRemoteTarget("", ""))
	require.NoError(t, validateRemoteTarget("coder1", ""))
	require.NoError(t, validateRemoteTarget("build_host.example", ""))
	require.ErrorContains(t, validateRemoteTarget("user@example.com", ""), "exact SSH alias")
	require.ErrorContains(t, validateRemoteTarget("example.com\nmalicious", ""), "exact SSH alias")
	require.ErrorContains(t, validateRemoteTarget("coder1", "/home/coder/.ssh/id_ed25519"), "not accepted")
}

func TestPathAncestorOf(t *testing.T) {
	t.Parallel()

	require.True(t, pathAncestorOf("/tmp/source", "/tmp/source/child"))
	require.True(t, pathAncestorOf("/tmp", "/tmp/source"))
	require.False(t, pathAncestorOf("/tmp/source", "/tmp/source"))
	require.False(t, pathAncestorOf("/tmp/source", "/tmp/source-other"))
}

func TestEndpointSSHInvocationQuotesAliasAndCommand(t *testing.T) {
	t.Parallel()

	invocation, err := endpointSSHInvocation(WorkspacePathEndpoint{
		Path: "/tmp/file",
		Host: "coder1",
	}, "cat '/tmp/file'")
	require.NoError(t, err)
	require.Contains(t, invocation, "'coder1'")
	require.Contains(t, invocation, "'cat '\"'\"'/tmp/file'\"'\"''")

	_, err = endpointSSHInvocation(WorkspacePathEndpoint{
		Path:         "/tmp/file",
		Host:         "coder1",
		IdentityFile: "/home/coder/.ssh/id_ed25519",
	}, "cat '/tmp/file'")
	require.ErrorContains(t, err, "not accepted")
}

func TestGitQueryCommand(t *testing.T) {
	t.Parallel()

	argv, err := gitQueryCommand(WorkspaceGitQueryArgs{
		Repo:      "/repo",
		Operation: "diff",
		Staged:    true,
		Paths:     []string{"src/main.go"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"git", "--no-optional-locks",
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=/dev/null",
		"-C", "/repo", "--no-pager",
		"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--ignore-submodules=all", "--cached", "--", "src/main.go",
	}, argv)

	argv, err = gitQueryCommand(WorkspaceGitQueryArgs{Repo: "/repo", Operation: "grep", Query: "TODO", Ref: "HEAD"})
	require.NoError(t, err)
	joined := strings.Join(argv, " ")
	require.Contains(t, joined, "--no-optional-locks")
	require.Contains(t, joined, "core.fsmonitor=false")
	require.Contains(t, joined, "core.hooksPath=/dev/null")
	require.Contains(t, joined, "-C /repo --no-pager grep")
	require.NotContains(t, argv, "fetch")
	require.NotContains(t, argv, "push")

	_, err = gitQueryCommand(WorkspaceGitQueryArgs{Repo: "/repo", Operation: "push"})
	require.ErrorContains(t, err, "unsupported")
}

func TestGitMutateCommand(t *testing.T) {
	t.Parallel()

	argv, err := gitMutateCommand(WorkspaceGitMutateArgs{
		Repo:      "/repo",
		Operation: "add",
		Paths:     []string{"-looks-like-option", "src/main.go"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"git", "-C", "/repo", "--no-pager", "add", "--", "-looks-like-option", "src/main.go"}, argv)

	argv, err = gitMutateCommand(WorkspaceGitMutateArgs{Repo: "/repo", Operation: "reset", Mode: "hard", Ref: "HEAD~1"})
	require.NoError(t, err)
	require.Equal(t, []string{"git", "-C", "/repo", "--no-pager", "reset", "--hard", "HEAD~1"}, argv)

	_, err = gitMutateCommand(WorkspaceGitMutateArgs{Repo: "/repo", Operation: "checkout", Ref: "--orphan"})
	require.ErrorContains(t, err, "cannot begin with '-'")

	_, err = gitMutateCommand(WorkspaceGitMutateArgs{Repo: "/repo", Operation: "push"})
	require.ErrorContains(t, err, "unsupported")
}

func TestSanitizeGitRemoteOutput(t *testing.T) {
	t.Parallel()

	input := "origin\thttps://token:secret@example.com/org/repo.git?sig=private#fragment (fetch)\n" +
		"origin\tssh://user:password@example.com/org/repo.git?key=private#fragment (push)\n" +
		"backup\tgit@example.com:org/repo.git (fetch)\n" +
		"helper\text::sh -c 'echo helper-secret' (fetch)\n"
	output := sanitizeGitRemoteOutput(input)
	require.NotContains(t, output, "token")
	require.NotContains(t, output, "secret")
	require.NotContains(t, output, "password")
	require.NotContains(t, output, "private")
	require.NotContains(t, output, "fragment")
	require.Contains(t, output, "https://example.com/org/repo.git")
	require.Contains(t, output, "ssh://example.com/org/repo.git")
	require.NotContains(t, output, "git@")
	require.Contains(t, output, "example.com:org/repo.git")
	require.NotContains(t, output, "helper-secret")
	require.Contains(t, output, "helper\t<redacted>")
}

func TestTruncateSemanticOutputProducesValidUTF8(t *testing.T) {
	t.Parallel()

	text, truncated := truncateSemanticOutput([]byte{'a', 0xff, 'b'}, 10)
	require.False(t, truncated)
	require.Equal(t, "a�b", text)
}
