package toolsdk

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

type WorkspaceExecArgs struct {
	Workspace     string            `json:"workspace"`
	Argv          []string          `json:"argv"`
	WorkDir       string            `json:"workdir,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Stdin         string            `json:"stdin,omitempty"`
	Host          string            `json:"host,omitempty"`
	IdentityFile  string            `json:"identity_file,omitempty"`
	WaitTimeoutMs *int              `json:"wait_timeout_ms,omitempty"`
}

type WorkspaceExecResult struct {
	Output     string                          `json:"output"`
	ExitCode   int                             `json:"exit_code"`
	ProcessID  string                          `json:"process_id"`
	Running    bool                            `json:"running"`
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

Exec has no process lifetime timeout. By default, the MCP call waits for completion up to the deployment-wide MCP tool timeout. Set wait_timeout_ms to return earlier; use 0 for an immediate post-start snapshot. If the process is still running when observation ends, the tool returns process_id, running=true, and the latest available output while the same durable process continues independently on the workspace Agent. While running=true, exit_code is only a legacy placeholder and MUST be ignored; it is not the process exit status, timeout, or failure. exit_code is meaningful only when running=false. Continue observing it with coder_workspace_process_output; do not start the command again. If process-start acknowledgement is lost, use coder_workspace_process_list before retrying because the process may already exist.

For commands that are expected to be long-running, expensive, side-effectful, or non-idempotent, prefer coder_workspace_process_start_v2 with argv so process_id is returned without waiting for process completion.`,
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
				"host": map[string]any{
					"type":        "string",
					"description": remoteHostDescription,
				},
				"identity_file": map[string]any{
					"type":        "string",
					"description": remoteIdentityFileDescription,
				},
				"wait_timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Optional observation interval in milliseconds. Omit to wait up to the deployment-wide MCP tool timeout; use 0 to return immediately after process start. This never limits process lifetime.",
					"minimum":     0,
				},
			},
			Required: []string{"workspace", "argv"},
		},
	},
	MCPAnnotations: mcpDestructiveOpenWorldAnnotations,
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
		if err := validateRemoteTarget(args.Host, args.IdentityFile); err != nil {
			return WorkspaceExecResult{}, err
		}
		wait, err := workspaceExecutionWaitDuration(args.WaitTimeoutMs, deps.MCPToolTimeoutMax())
		if err != nil {
			return WorkspaceExecResult{}, err
		}
		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return WorkspaceExecResult{}, err
		}
		defer conn.Close()

		started, err := startWorkspaceProcessWithinObservation(ctx, conn, workspacesdk.StartProcessRequest{
			Argv:         args.Argv,
			WorkDir:      args.WorkDir,
			Env:          args.Env,
			Host:         args.Host,
			IdentityFile: args.IdentityFile,
			Tool:         InvocationToolFromContext(ctx),
			Stdin:        args.Stdin,
		}, budget)
		if err != nil {
			return WorkspaceExecResult{}, xerrors.Errorf("start workspace exec: %w", err)
		}

		// Bound only the MCP observation. The Agent-tracked process is durable and
		// continues after this window if it has not exited yet.
		resp, waitErr := observeWorkspaceProcess(ctx, conn, started.ID, budget, wait)
		if waitErr != nil {
			return WorkspaceExecResult{}, waitErr
		}
		result := workspaceProcessResult(started.ID, resp, argvAdvisories(args.Argv))
		return WorkspaceExecResult{
			Output:     result.Output,
			ExitCode:   result.ExitCode,
			ProcessID:  result.ProcessID,
			Running:    result.Running,
			Truncated:  result.Truncated,
			Advisories: result.Advisories,
		}, nil
	},
}
