//go:build !windows

package agentssh

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"

	gliderssh "github.com/gliderlabs/ssh"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/agent/agentexec"
	"github.com/coder/coder/v2/pty"
	"github.com/coder/coder/v2/testutil"
)

const longScript = `
echo "started"
sleep 30
echo "done"
`

// Test_sessionStart_orphan tests running a command that takes a long time to
// exit normally, and terminate the SSH session context early to verify that we
// return quickly and don't leave the command running as an "orphan" with no
// active SSH session.
func Test_sessionStart_orphan(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitMedium)
	defer cancel()
	logger := testutil.Logger(t)
	s, err := NewServer(ctx, logger, prometheus.NewRegistry(), afero.NewMemMapFs(), agentexec.DefaultExecer, nil)
	require.NoError(t, err)
	defer s.Close()
	err = s.UpdateHostSigner(42)
	assert.NoError(t, err)

	// Here we're going to call the handler directly with a faked SSH session
	// that just uses io.Pipes instead of a network socket.  There is a large
	// variation in the time between closing the socket from the client side and
	// the SSH server canceling the session Context, which would lead to a flaky
	// test if we did it that way.  So instead, we directly cancel the context
	// in this test.
	sessionCtx, sessionCancel := context.WithCancel(ctx)
	toClient, fromClient, sess := newTestSession(sessionCtx)
	ptyInfo := gliderssh.Pty{}
	windowSize := make(chan gliderssh.Window)
	close(windowSize)
	// the command gets the session context so that Go will terminate it when
	// the session expires.
	cmd := pty.CommandContext(sessionCtx, "sh", "-c", longScript)

	done := make(chan struct{})
	go func() {
		defer close(done)

		// we don't really care what the error is here.  In the larger scenario,
		// the client has disconnected, so we can't return any error information
		// to them.
		_ = s.startPTYSession(logger, sess, MagicSessionTypeSSH, "ssh", "", cmd, ptyInfo, windowSize)
	}()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		s := bufio.NewScanner(toClient)
		assert.True(t, s.Scan())
		txt := s.Text()
		assert.Equal(t, "started", txt, "output corrupted")
	}()

	waitForChan(ctx, t, readDone, "read timeout")
	// process is started, and should be sleeping for ~30 seconds

	sessionCancel()

	// now, we wait for the handler to complete.  If it does so before the
	// main test timeout, we consider this a pass.  If not, it indicates
	// that the server isn't properly shutting down sessions when they are
	// disconnected client side, which could lead to processes hanging around
	// indefinitely.
	waitForChan(ctx, t, done, "handler timeout")

	err = fromClient.Close()
	require.NoError(t, err)
}

func Test_startCommandActivity(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), testutil.WaitMedium)
	defer cancel()
	var calls int
	var command string
	var argv []string
	var workDir string
	var tool string
	var exitCode int
	var sessionTypes []MagicSessionType
	s, err := NewServer(ctx, testutil.Logger(t), prometheus.NewRegistry(), afero.NewMemMapFs(), agentexec.DefaultExecer, &Config{
		ReportCommandActivity: func(sessionType MagicSessionType, gotCommand string, gotArgv []string, gotWorkDir, gotTool string) func(int) {
			calls++
			sessionTypes = append(sessionTypes, sessionType)
			command = gotCommand
			argv = append([]string(nil), gotArgv...)
			workDir = gotWorkDir
			tool = gotTool
			return func(gotExitCode int) { exitCode = gotExitCode }
		},
	})
	require.NoError(t, err)
	defer s.Close()

	finish := s.startCommandActivity("echo hello", "/workspace", MagicSessionTypeSSH, "bash")
	finish(7)
	require.Equal(t, 1, calls)
	require.Equal(t, "echo hello", command)
	require.Empty(t, argv)
	require.Equal(t, "/workspace", workDir)
	require.Equal(t, "bash", tool)
	require.Equal(t, 7, exitCode)

	s.startCommandActivity("", "/workspace", MagicSessionTypeSSH, "bash")(0)
	s.startCommandActivity("code --wait", "/workspace", MagicSessionTypeVSCode, "")(0)
	s.startCommandActivity("idea", "/workspace", MagicSessionTypeJetBrains, "")(0)
	require.Equal(t, 3, calls, "non-empty commands must be recorded regardless of SSH session channel")
	require.Equal(t, []MagicSessionType{MagicSessionTypeSSH, MagicSessionTypeVSCode, MagicSessionTypeJetBrains}, sessionTypes)
}

func TestExtractCommandActivityTool(t *testing.T) {
	t.Parallel()

	tool, env := extractCommandActivityTool([]string{
		"KEEP=yes",
		"CODER_MCP_TOOL=bash",
		"OTHER=value",
	})
	require.Equal(t, "bash", tool)
	require.Equal(t, []string{"KEEP=yes", "OTHER=value"}, env)

	tool, env = extractCommandActivityTool([]string{
		"CODER_MCP_TOOL=exec",
		"KEEP=yes",
	})
	require.Empty(t, tool, "SSH clients must not be able to spoof another MCP tool")
	require.Equal(t, []string{"KEEP=yes"}, env, "attribution metadata must never reach the user command environment")
}

func waitForChan(ctx context.Context, t *testing.T, c <-chan struct{}, msg string) {
	t.Helper()
	select {
	case <-c:
		// OK!
	case <-ctx.Done():
		t.Fatal(msg)
	}
}

type testSession struct {
	ctx testSSHContext

	// c2p is the client -> pty buffer
	toPty *io.PipeReader
	// p2c is the pty -> client buffer
	fromPty *io.PipeWriter
}

type testSSHContext struct {
	context.Context
}

var (
	_ gliderssh.Context = testSSHContext{}
	_ ptySession        = &testSession{}
)

func newTestSession(ctx context.Context) (toClient *io.PipeReader, fromClient *io.PipeWriter, s ptySession) {
	toClient, fromPty := io.Pipe()
	toPty, fromClient := io.Pipe()

	return toClient, fromClient, &testSession{
		ctx:     testSSHContext{ctx},
		toPty:   toPty,
		fromPty: fromPty,
	}
}

func (s *testSession) Context() gliderssh.Context {
	return s.ctx
}

func (*testSession) DisablePTYEmulation() {}

// RawCommand returns "quiet logon" so that the PTY handler doesn't attempt to
// write the message of the day, which will interfere with our tests.  It writes
// the message of the day if it's a shell login (zero length RawCommand()).
func (*testSession) RawCommand() string { return "quiet logon" }

func (s *testSession) Read(p []byte) (n int, err error) {
	return s.toPty.Read(p)
}

func (s *testSession) Write(p []byte) (n int, err error) {
	return s.fromPty.Write(p)
}

func (*testSession) Signals(_ chan<- gliderssh.Signal) {
	// Not implemented, but will be called.
}

func (testSSHContext) Lock() {
	panic("not implemented")
}

func (testSSHContext) Unlock() {
	panic("not implemented")
}

// User returns the username used when establishing the SSH connection.
func (testSSHContext) User() string {
	panic("not implemented")
}

// SessionID returns the session hash.
func (testSSHContext) SessionID() string {
	panic("not implemented")
}

// ClientVersion returns the version reported by the client.
func (testSSHContext) ClientVersion() string {
	panic("not implemented")
}

// ServerVersion returns the version reported by the server.
func (testSSHContext) ServerVersion() string {
	panic("not implemented")
}

// RemoteAddr returns the remote address for this connection.
func (testSSHContext) RemoteAddr() net.Addr {
	panic("not implemented")
}

// LocalAddr returns the local address for this connection.
func (testSSHContext) LocalAddr() net.Addr {
	panic("not implemented")
}

// Permissions returns the Permissions object used for this connection.
func (testSSHContext) Permissions() *gliderssh.Permissions {
	panic("not implemented")
}

// SetValue allows you to easily write new values into the underlying context.
func (testSSHContext) SetValue(_, _ interface{}) {
	panic("not implemented")
}

func (testSSHContext) KeepAlive() *gliderssh.SessionKeepAlive {
	panic("not implemented")
}
