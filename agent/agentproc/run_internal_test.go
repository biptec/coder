//go:build !windows

package agentproc

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"cdr.dev/slog/v3/sloggers/slogtest"
	"github.com/coder/coder/v2/agent/agentexec"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
	"github.com/coder/coder/v2/testutil"
)

func TestRunCommandCancellationKillsProcessGroup(t *testing.T) {
	t.Parallel()
	logger := slogtest.Make(t, &slogtest.Options{IgnoreErrors: true})
	api := NewAPI(logger, agentexec.DefaultExecer, nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = api.Close() })

	pidFile := t.TempDir() + "/child.pid"
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := api.runCommand(ctx, workspacesdk.RunCommandRequest{
			Command: "sleep 30 & echo $! > " + shellQuote(pidFile) + "; wait",
		})
		result <- err
	}()

	var childPID int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			return false
		}
		childPID = pid
		return true
	}, testutil.WaitLong, testutil.IntervalFast)

	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(testutil.WaitLong):
		t.Fatal("runCommand did not return after cancellation")
	}

	require.Eventually(t, func() bool {
		err := syscall.Kill(childPID, 0)
		return errors.Is(err, syscall.ESRCH)
	}, testutil.WaitLong, testutil.IntervalFast, "child process should be killed with the process group")
}
