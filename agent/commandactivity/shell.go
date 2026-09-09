package commandactivity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/pty"
)

// InteractiveShellReporter starts a command activity record and returns a
// function that completes it with the command's exit code.
type InteractiveShellReporter func(command, workDir string) func(exitCode int)

// InteractiveShellTracker owns the private shell integration files and the
// event tailer for one interactive shell. Close is idempotent.
type InteractiveShellTracker struct {
	cancel context.CancelFunc
	done   chan struct{}
	dir    string
	once   sync.Once
}

// PrepareInteractiveShell installs transparent command activity hooks for a
// supported interactive shell. The command is modified in-place before it is
// started. Unsupported shells are left unchanged and return (nil, false, nil).
//
// The integration deliberately uses a private file instead of terminal escape
// sequences. This keeps activity metadata out of user-visible PTY output and
// avoids treating arbitrary terminal input as a command.
func PrepareInteractiveShell(logger slog.Logger, cmd *pty.Cmd, reporter InteractiveShellReporter) (*InteractiveShellTracker, bool, error) {
	if cmd == nil || reporter == nil || runtime.GOOS == "windows" {
		return nil, false, nil
	}

	shell := filepath.Base(cmd.Path)
	if shell != "bash" && shell != "zsh" {
		return nil, false, nil
	}

	dir, err := os.MkdirTemp("", "coder-command-activity-")
	if err != nil {
		return nil, false, fmt.Errorf("create command activity directory: %w", err)
	}
	cleanupOnError := func() {
		_ = os.RemoveAll(dir)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		cleanupOnError()
		return nil, false, fmt.Errorf("chmod command activity directory: %w", err)
	}

	eventPath := filepath.Join(dir, "events")
	f, err := os.OpenFile(eventPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		cleanupOnError()
		return nil, false, fmt.Errorf("create command activity event file: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanupOnError()
		return nil, false, fmt.Errorf("close command activity event file: %w", err)
	}

	switch shell {
	case "bash":
		if err := prepareBash(cmd, dir, eventPath); err != nil {
			cleanupOnError()
			return nil, false, err
		}
	case "zsh":
		if err := prepareZsh(cmd, dir, eventPath); err != nil {
			cleanupOnError()
			return nil, false, err
		}
	}

	ctx := cmd.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	tracker := &InteractiveShellTracker{
		cancel: cancel,
		done:   make(chan struct{}),
		dir:    dir,
	}
	go func() {
		defer close(tracker.done)
		tailInteractiveShellEvents(ctx, logger, eventPath, reporter)
	}()
	return tracker, true, nil
}

func (t *InteractiveShellTracker) Close() {
	if t == nil {
		return
	}
	t.once.Do(func() {
		t.cancel()
		<-t.done
		_ = os.RemoveAll(t.dir)
	})
}

func prepareBash(cmd *pty.Cmd, dir, eventPath string) error {
	realHome := envValue(cmd.Env, "HOME")
	if realHome == "" {
		var err error
		realHome, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve bash home: %w", err)
		}
	}

	wrapperHome := filepath.Join(dir, "home")
	if err := os.Mkdir(wrapperHome, 0o700); err != nil {
		return fmt.Errorf("create bash wrapper home: %w", err)
	}
	profile := filepath.Join(wrapperHome, ".bash_profile")
	content := fmt.Sprintf(`export HOME=%s
if [[ -r "$HOME/.bash_profile" ]]; then
	. "$HOME/.bash_profile"
elif [[ -r "$HOME/.bash_login" ]]; then
	. "$HOME/.bash_login"
elif [[ -r "$HOME/.profile" ]]; then
	. "$HOME/.profile"
fi

__CODER_COMMAND_ACTIVITY_FILE=%s
__coder_command_activity_ready=0
	__coder_command_activity_active=0
	__coder_command_activity_prompt_histcmd=0
	__coder_command_activity_debug() {
		local __coder_status=$?
	if (( __coder_command_activity_ready )); then
		__coder_command_activity_ready=0
			# Commands intentionally excluded from Bash history (for example by
			# HISTCONTROL=ignorespace) are not recorded. It is safer to respect the
			# user's history policy than to attribute the previous history entry.
			if [[ "$HISTCMD" == "$__coder_command_activity_prompt_histcmd" ]]; then
				local __coder_line __coder_command
				__coder_line="$(HISTTIMEFORMAT= builtin history 1 2>/dev/null)" || return "$__coder_status"
				if [[ "$__coder_line" =~ ^[[:space:]]*[0-9]+[[:space:]]+(.*)$ ]]; then
					__coder_command="${BASH_REMATCH[1]}"
				fi
				if [[ -n ${__coder_command-} ]]; then
					builtin printf 'S\0%%s\0%%s\0' "$__coder_command" "$PWD" >> "$__CODER_COMMAND_ACTIVITY_FILE" 2>/dev/null || :
					__coder_command_activity_active=1
				fi
			fi
		fi
	return "$__coder_status"
}
__coder_command_activity_prompt_capture() {
	local __coder_status=$?
	if (( __coder_command_activity_active )); then
		builtin printf 'F\0%%d\0' "$__coder_status" >> "$__CODER_COMMAND_ACTIVITY_FILE" 2>/dev/null || :
		__coder_command_activity_active=0
	fi
	__coder_command_activity_last_status=$__coder_status
	return "$__coder_status"
}
__coder_command_activity_prompt_arm() {
	local __coder_status=${__coder_command_activity_last_status:-0}
	__coder_command_activity_prompt_histcmd=$HISTCMD
	__coder_command_activity_ready=1
	return "$__coder_status"
}
# Do not replace an existing DEBUG trap. Debugger/preexec frameworks depend
# on exact DEBUG semantics, so a safe no-op is preferable to breaking them.
if [[ -z $(trap -p DEBUG) ]]; then
	if [[ $(declare -p PROMPT_COMMAND 2>/dev/null) == "declare -a"* ]]; then
		PROMPT_COMMAND=(__coder_command_activity_prompt_capture "${PROMPT_COMMAND[@]}" __coder_command_activity_prompt_arm)
	elif [[ -n ${PROMPT_COMMAND-} ]]; then
		PROMPT_COMMAND="__coder_command_activity_prompt_capture;${PROMPT_COMMAND};__coder_command_activity_prompt_arm"
	else
		PROMPT_COMMAND="__coder_command_activity_prompt_capture;__coder_command_activity_prompt_arm"
	fi
	trap '__coder_command_activity_debug' DEBUG
fi
`, shellQuote(realHome), shellQuote(eventPath))
	if err := os.WriteFile(profile, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write bash command activity profile: %w", err)
	}
	cmd.Env = setEnv(cmd.Env, "HOME", wrapperHome)
	return nil
}

func prepareZsh(cmd *pty.Cmd, dir, eventPath string) error {
	realHome := envValue(cmd.Env, "HOME")
	if realHome == "" {
		var err error
		realHome, err = os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve zsh home: %w", err)
		}
	}
	userZDOTDIR := envValue(cmd.Env, "ZDOTDIR")
	if userZDOTDIR == "" {
		userZDOTDIR = realHome
	}

	phase := func(name string, installHooks, final bool) string {
		var b strings.Builder
		fmt.Fprintf(&b, "typeset -g __CODER_COMMAND_ACTIVITY_USER_ZDOTDIR=${__CODER_COMMAND_ACTIVITY_USER_ZDOTDIR:-%s}\n", shellQuote(userZDOTDIR))
		fmt.Fprintf(&b, "typeset -g __CODER_COMMAND_ACTIVITY_WRAPPER_ZDOTDIR=%s\n", shellQuote(dir))
		fmt.Fprintf(&b, "if [[ -r \"$__CODER_COMMAND_ACTIVITY_USER_ZDOTDIR/%s\" ]]; then\n", name)
		b.WriteString("\tZDOTDIR=\"$__CODER_COMMAND_ACTIVITY_USER_ZDOTDIR\"\n")
		fmt.Fprintf(&b, "\t. \"$ZDOTDIR/%s\"\n", name)
		b.WriteString("\t__CODER_COMMAND_ACTIVITY_USER_ZDOTDIR=${ZDOTDIR:-$HOME}\n")
		b.WriteString("fi\n")
		if installHooks {
			fmt.Fprintf(&b, "typeset -g __CODER_COMMAND_ACTIVITY_FILE=%s\n", shellQuote(eventPath))
			b.WriteString(`__coder_command_activity_preexec() {
	local __coder_status=$?
	local __coder_command="$1"
	[[ -n "$__coder_command" ]] && builtin printf 'S\0%s\0%s\0' "$__coder_command" "$PWD" >> "$__CODER_COMMAND_ACTIVITY_FILE" 2>/dev/null
	return "$__coder_status"
}
__coder_command_activity_precmd() {
	local __coder_status=$?
	builtin printf 'F\0%d\0' "$__coder_status" >> "$__CODER_COMMAND_ACTIVITY_FILE" 2>/dev/null
	return "$__coder_status"
}
__coder_command_activity_restore_zdotdir() {
	local __coder_status=$?
	ZDOTDIR="$__CODER_COMMAND_ACTIVITY_USER_ZDOTDIR"
	precmd_functions=(${precmd_functions:#__coder_command_activity_restore_zdotdir})
	return "$__coder_status"
}
preexec_functions=(__coder_command_activity_preexec ${preexec_functions:#__coder_command_activity_preexec})
precmd_functions=(${precmd_functions:#__coder_command_activity_restore_zdotdir})
precmd_functions=(${precmd_functions:#__coder_command_activity_precmd})
precmd_functions=(__coder_command_activity_restore_zdotdir __coder_command_activity_precmd ${precmd_functions[@]})
`)
		}
		if final {
			b.WriteString("ZDOTDIR=\"$__CODER_COMMAND_ACTIVITY_USER_ZDOTDIR\"\n")
		} else {
			b.WriteString("ZDOTDIR=\"$__CODER_COMMAND_ACTIVITY_WRAPPER_ZDOTDIR\"\n")
		}
		return b.String()
	}

	files := map[string]string{
		".zshenv":   phase(".zshenv", false, false),
		".zprofile": phase(".zprofile", false, false),
		".zshrc":    phase(".zshrc", true, false),
		".zlogin":   phase(".zlogin", false, true),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			return fmt.Errorf("write zsh command activity %s: %w", name, err)
		}
	}
	cmd.Env = setEnv(cmd.Env, "ZDOTDIR", dir)
	return nil
}

func tailInteractiveShellEvents(ctx context.Context, logger slog.Logger, path string, reporter InteractiveShellReporter) {
	f, err := os.Open(path)
	if err != nil {
		logger.Warn(ctx, "open interactive shell activity file", slog.Error(err))
		return
	}
	defer f.Close()

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	var pending []byte
	var fields []string
	var finish func(int)
	consume := func(data []byte) {
		pending = append(pending, data...)
		for {
			idx := bytes.IndexByte(pending, 0)
			if idx < 0 {
				break
			}
			fields = append(fields, string(pending[:idx]))
			pending = pending[idx+1:]

		processFields:
			for len(fields) > 0 {
				switch fields[0] {
				case "S":
					if len(fields) < 3 {
						break processFields
					}
					if finish != nil {
						finish(-1)
					}
					command, workDir := fields[1], fields[2]
					fields = fields[3:]
					if strings.TrimSpace(command) != "" {
						finish = reporter(command, workDir)
					} else {
						finish = nil
					}
				case "F":
					if len(fields) < 2 {
						break processFields
					}
					rawExitCode := fields[1]
					exitCode, parseErr := strconv.Atoi(rawExitCode)
					fields = fields[2:]
					if parseErr != nil {
						logger.Debug(ctx, "ignore invalid interactive shell exit code", slog.F("exit_code", rawExitCode))
						continue
					}
					if finish != nil {
						finish(exitCode)
						finish = nil
					}
				default:
					fields = fields[1:]
				}
			}
		}
	}

	buf := make([]byte, 8192)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			consume(buf[:n])
		}
		switch {
		case readErr == nil:
			continue
		case errors.Is(readErr, io.EOF):
			select {
			case <-ctx.Done():
				if finish != nil {
					finish(-1)
				}
				return
			case <-ticker.C:
			}
		default:
			logger.Warn(ctx, "read interactive shell activity file", slog.Error(readErr))
			if finish != nil {
				finish(-1)
			}
			return
		}
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			continue
		}
		out = append(out, item)
	}
	return append(out, prefix+value)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
