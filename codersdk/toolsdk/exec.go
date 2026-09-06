package toolsdk

import (
	"context"
	"errors"
	"time"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

type WorkspaceExecArgs struct {
	Workspace string            `json:"workspace"`
	Argv      []string          `json:"argv"`
	WorkDir   string            `json:"workdir,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Stdin     string            `json:"stdin,omitempty"`
	TimeoutMs int               `json:"timeout_ms,omitempty"`
}

type WorkspaceExecResult struct {
	Output     string                          `json:"output"`
	ExitCode   int                             `json:"exit_code"`
	ProcessID  string                          `json:"process_id"`
	Running    bool                            `json:"running"`
	TimedOut   bool                            `json:"timed_out,omitempty"`
	Truncated  *workspacesdk.ProcessTruncation `json:"truncated,omitempty"`
	Advisories []ToolAdvisory                  `json:"advisories,omitempty"`
}

var WorkspaceExec = Tool[WorkspaceExecArgs, WorkspaceExecResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceExec,
		Description: `Execute a program directly in a Coder workspace without shell parsing.

argv[0] is the executable and every later element is passed as exactly one argument.
Use this tool instead of bash whenever shell syntax (pipes, redirects, &&, loops, expansions)
is not intentionally required. This avoids JSON -> shell -> quoting ambiguity.

For long-running, expensive, side-effectful, or non-idempotent commands, use
coder_workspace_process_start_v2 with argv instead so execution can be recovered by process_id.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "The workspace name in format [owner/]workspace[.agent].",
				},
				"argv": map[string]any{
					"type":        "array",
					"description": "Executable and arguments. No shell parsing is performed.",
					"minItems":    1,
					"items":       map[string]any{"type": "string"},
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "Optional working directory.",
				},
				"env": map[string]any{
					"type":                 "object",
					"description":          "Optional environment variable overrides.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"stdin": map[string]any{
					"type":        "string",
					"description": "Optional stdin delivered once, followed by EOF. Maximum 1 MiB.",
					"maxLength":   workspacesdk.MaxProcessInputBytes,
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Execution timeout in milliseconds. Defaults to 60000 and cannot exceed 300000.",
					"default":     60000,
					"minimum":     1,
					"maximum":     300000,
				},
			},
			Required: []string{"workspace", "argv"},
		},
	},
	MCPAnnotations: mcpDestructiveAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceExecArgs) (WorkspaceExecResult, error) {
		if args.Workspace == "" {
			return WorkspaceExecResult{}, xerrors.New("workspace name cannot be empty")
		}
		if len(args.Argv) == 0 || args.Argv[0] == "" {
			return WorkspaceExecResult{}, xerrors.New("argv must contain a non-empty executable at argv[0]")
		}
		if len(args.Stdin) > workspacesdk.MaxProcessInputBytes {
			return WorkspaceExecResult{}, xerrors.Errorf("stdin cannot exceed %d bytes", workspacesdk.MaxProcessInputBytes)
		}
		timeoutMs := args.TimeoutMs
		if timeoutMs <= 0 {
			timeoutMs = 60000
		}
		if timeoutMs > 300000 {
			return WorkspaceExecResult{}, xerrors.New("timeout_ms cannot exceed 300000")
		}

		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceExecResult{}, err
		}
		defer conn.Close()

		started, err := conn.StartProcess(ctx, workspacesdk.StartProcessRequest{
			Argv:    args.Argv,
			WorkDir: args.WorkDir,
			Env:     args.Env,
			Stdin:   args.Stdin,
		})
		if err != nil {
			return WorkspaceExecResult{}, xerrors.Errorf("start workspace exec: %w", err)
		}

		execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
		resp, waitErr := conn.ProcessOutput(execCtx, started.ID, &workspacesdk.ProcessOutputOptions{Wait: true})
		cancel()
		if waitErr == nil {
			result := workspaceProcessResult(started.ID, resp, argvAdvisories(args.Argv))
			return WorkspaceExecResult{
				Output:     result.Output,
				ExitCode:   result.ExitCode,
				ProcessID:  result.ProcessID,
				Running:    result.Running,
				Truncated:  result.Truncated,
				Advisories: result.Advisories,
			}, nil
		}
		if ctx.Err() != nil {
			return WorkspaceExecResult{}, waitErr
		}
		if !errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return WorkspaceExecResult{}, xerrors.Errorf("wait for workspace exec: %w", waitErr)
		}

		signalCtx, signalCancel := context.WithTimeout(context.Background(), processSnapshotTimeout)
		_ = conn.SignalProcess(signalCtx, started.ID, "terminate")
		signalCancel()
		snapshotCtx, snapshotCancel := context.WithTimeout(context.Background(), processSnapshotTimeout)
		defer snapshotCancel()
		snapshot, snapshotErr := conn.ProcessOutput(snapshotCtx, started.ID, nil)
		if snapshotErr != nil {
			return WorkspaceExecResult{
				ExitCode:   124,
				ProcessID:  started.ID,
				Running:    true,
				TimedOut:   true,
				Advisories: argvAdvisories(args.Argv),
			}, nil
		}
		result := workspaceProcessResult(started.ID, snapshot, argvAdvisories(args.Argv))
		return WorkspaceExecResult{
			Output:     result.Output,
			ExitCode:   124,
			ProcessID:  result.ProcessID,
			Running:    result.Running,
			TimedOut:   true,
			Truncated:  result.Truncated,
			Advisories: result.Advisories,
		}, nil
	},
}
