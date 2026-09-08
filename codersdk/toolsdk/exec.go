package toolsdk

import (
	"context"

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
}

type WorkspaceExecResult struct {
	Output     string                          `json:"output"`
	ExitCode   int                             `json:"exit_code"`
	ProcessID  string                          `json:"process_id"`
	Running    bool                            `json:"running"`
	Truncated  *workspacesdk.ProcessTruncation `json:"truncated,omitempty"`
	Advisories []ToolAdvisory                  `json:"advisories,omitempty"`
}

func waitForExecCompletion(ctx context.Context, conn workspacesdk.AgentConn, processID string) (workspacesdk.ProcessOutputResponse, error) {
	for {
		resp, err := conn.ProcessOutput(ctx, processID, &workspacesdk.ProcessOutputOptions{Wait: true})
		if err != nil {
			return workspacesdk.ProcessOutputResponse{}, xerrors.Errorf("wait for workspace exec: %w", err)
		}
		if !resp.Running {
			return resp, nil
		}
	}
}

var WorkspaceExec = Tool[WorkspaceExecArgs, WorkspaceExecResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceExec,
		Description: `Execute a program directly in a Coder workspace without shell parsing.

argv[0] is the executable and every later element is passed as exactly one argument.
Use this tool instead of bash whenever shell syntax (pipes, redirects, &&, loops, expansions)
is not intentionally required. This avoids JSON -> shell -> quoting ambiguity.

Exec has no execution timeout. It waits until the process exits or the MCP caller cancels the request.
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
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceExecResult{}, err
		}
		defer conn.Close()

		started, err := conn.StartProcess(ctx, workspacesdk.StartProcessRequest{
			Argv:    args.Argv,
			WorkDir: args.WorkDir,
			Env:     args.Env,
			Tool:    InvocationToolFromContext(ctx),
			Stdin:   args.Stdin,
		})
		if err != nil {
			return WorkspaceExecResult{}, xerrors.Errorf("start workspace exec: %w", err)
		}

		// ProcessOutput deliberately caps each individual blocking agent request
		// so a stale transport cannot hold a server handler forever. Exec itself
		// has no timeout: if the agent returns a still-running snapshot at that
		// safety boundary, wait again until the process exits or the caller
		// cancels the MCP request.
		resp, waitErr := waitForExecCompletion(ctx, conn, started.ID)
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
