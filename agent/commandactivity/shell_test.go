package commandactivity

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/pty"
	"github.com/coder/coder/v2/testutil"
)

type recordedShellCommand struct {
	command  string
	workDir  string
	exitCode *int
}

type shellRecorder struct {
	mu      sync.Mutex
	records []*recordedShellCommand
}

func (r *shellRecorder) report(command, workDir string) func(int) {
	r.mu.Lock()
	record := &recordedShellCommand{command: command, workDir: workDir}
	r.records = append(r.records, record)
	r.mu.Unlock()
	return func(exitCode int) {
		r.mu.Lock()
		defer r.mu.Unlock()
		code := exitCode
		record.exitCode = &code
	}
}

func (r *shellRecorder) snapshot() []recordedShellCommand {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]recordedShellCommand, 0, len(r.records))
	for _, record := range r.records {
		copy := *record
		result = append(result, copy)
	}
	return result
}

func findRecordedCommand(records []recordedShellCommand, command string) (recordedShellCommand, bool) {
	for _, record := range records {
		if strings.TrimSpace(record.command) == command {
			return record, true
		}
	}
	return recordedShellCommand{}, false
}

func TestTailInteractiveShellEvents(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "events")
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	recorder := &shellRecorder{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		tailInteractiveShellEvents(ctx, testutil.Logger(t), path, recorder.report)
	}()

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	require.NoError(t, err)
	_, err = file.Write([]byte("S\x00echo hel"))
	require.NoError(t, err)
	_, err = file.Write([]byte("lo\x00/tmp\x00F\x000\x00S\x00false\x00/work\x00F\x001\x00"))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	require.Eventually(t, func() bool {
		records := recorder.snapshot()
		return len(records) == 2 && records[0].exitCode != nil && records[1].exitCode != nil
	}, testutil.WaitMedium, 10*time.Millisecond)

	records := recorder.snapshot()
	require.Equal(t, "echo hello", records[0].command)
	require.Equal(t, "/tmp", records[0].workDir)
	require.Equal(t, 0, *records[0].exitCode)
	require.Equal(t, "false", records[1].command)
	require.Equal(t, "/work", records[1].workDir)
	require.Equal(t, 1, *records[1].exitCode)

	cancel()
	select {
	case <-done:
	case <-time.After(testutil.WaitMedium):
		t.Fatal("event tailer did not stop")
	}
}

func TestPrepareInteractiveShellUnsupported(t *testing.T) {
	t.Parallel()

	cmd := pty.CommandContext(t.Context(), "/bin/sh")
	tracker, enabled, err := PrepareInteractiveShell(testutil.Logger(t), cmd, func(string, string) func(int) {
		return func(int) {}
	})
	require.NoError(t, err)
	require.False(t, enabled)
	require.Nil(t, tracker)
}

func TestInteractiveShellCommandActivity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell integration is currently Unix-only")
	}

	for _, shellName := range []string{"bash", "zsh"} {
		shellName := shellName
		t.Run(shellName, func(t *testing.T) {
			shellPath, err := exec.LookPath(shellName)
			if err != nil {
				t.Skipf("%s is not installed", shellName)
			}

			realHome := t.TempDir()
			switch shellName {
			case "bash":
				require.NoError(t, os.WriteFile(filepath.Join(realHome, ".bash_profile"), []byte("export HISTFILE=/dev/null\nexport PS1='coder-test$ '\nPROMPT_COMMAND=':'\n"), 0o600))
			case "zsh":
				require.NoError(t, os.WriteFile(filepath.Join(realHome, ".zshrc"), []byte("HISTFILE=/dev/null\nPROMPT='coder-test% '\n"), 0o600))
			}

			ctx, cancel := context.WithTimeout(t.Context(), testutil.WaitLong)
			defer cancel()
			cmd := pty.CommandContext(ctx, shellPath, "-l")
			cmd.Dir = realHome
			cmd.Env = append(os.Environ(), "HOME="+realHome, "TERM=xterm", "SHELL="+shellPath)

			recorder := &shellRecorder{}
			tracker, enabled, err := PrepareInteractiveShell(testutil.Logger(t), cmd, recorder.report)
			require.NoError(t, err)
			require.True(t, enabled)
			defer tracker.Close()

			// Use the generated Bash profile as an rcfile in the test to avoid
			// machine-specific /etc/profile behavior. Production login shells still
			// source the same generated .bash_profile normally.
			if shellName == "bash" {
				profile := filepath.Join(tracker.dir, "home", ".bash_profile")
				cmd.Args = []string{shellPath, "--noprofile", "--rcfile", profile, "-i"}
			} else {
				cmd.Args = []string{shellPath, "-i"}
			}

			ptty, process, err := pty.Start(cmd)
			require.NoError(t, err)
			defer ptty.Close()
			outputDone := make(chan struct{})
			go func() {
				defer close(outputDone)
				_, _ = io.Copy(io.Discard, ptty.OutputReader())
			}()

			writer := ptty.InputWriter()
			commands := "printf 'first\\n'\nfalse\ncd /tmp\npwd\n"
			if shellName == "zsh" {
				commands += "test \"$ZDOTDIR\" = \"$HOME\"\n"
			}
			_, err = io.WriteString(writer, commands)
			require.NoError(t, err)

			require.Eventually(t, func() bool {
				records := recorder.snapshot()
				first, firstOK := findRecordedCommand(records, "printf 'first\\n'")
				failed, failedOK := findRecordedCommand(records, "false")
				cd, cdOK := findRecordedCommand(records, "cd /tmp")
				pwd, pwdOK := findRecordedCommand(records, "pwd")
				ready := firstOK && first.exitCode != nil && *first.exitCode == 0 &&
					failedOK && failed.exitCode != nil && *failed.exitCode == 1 &&
					cdOK && cd.exitCode != nil && *cd.exitCode == 0 &&
					pwdOK && pwd.exitCode != nil && *pwd.exitCode == 0 && pwd.workDir == "/tmp"
				if shellName == "zsh" {
					zdot, zdotOK := findRecordedCommand(records, `test "$ZDOTDIR" = "$HOME"`)
					ready = ready && zdotOK && zdot.exitCode != nil && *zdot.exitCode == 0
				}
				return ready
			}, testutil.WaitLong, 20*time.Millisecond)

			_, err = io.WriteString(writer, "exit\n")
			require.NoError(t, err)
			_ = process.Wait()
			_ = ptty.Close()
			select {
			case <-outputDone:
			case <-time.After(testutil.WaitMedium):
				t.Fatal("PTY output reader did not stop")
			}
		})
	}
}
