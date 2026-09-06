package toolsdk

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

// WorkspaceProcessStartArgs is the legacy/full-catalog command-only schema.
// Developer MCP uses WorkspaceProcessStartV2 so this contract remains stable
// for Admin and existing integrations.
type WorkspaceProcessStartArgs struct {
	Workspace  string            `json:"workspace"`
	Command    string            `json:"command"`
	WorkDir    string            `json:"workdir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Background bool              `json:"background,omitempty"`
}

var WorkspaceProcessStart = Tool[WorkspaceProcessStartArgs, WorkspaceProcessStartResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceProcessStart,
		Description: `Start a durable process in a Coder workspace and return its process_id immediately.

Use this tool instead of coder_workspace_bash when a command may run for a long time, is expensive, has side effects, or must not be executed twice. This tool starts the command exactly once and does not wait for completion or return command output. After a successful start, use coder_workspace_process_output with the returned process_id to observe the same process.

If this call ends with a timeout, 502, disconnect, or any other uncertain result after submission, DO NOT start the command again. First call coder_workspace_process_list for the same workspace and recover the existing process by matching its command, workdir, and start time.

In the standard Developer Workspace, only /home/coder is persistent across workspace recreation. The system filesystem outside /home/coder is ephemeral. Prefer durable tools and dependencies under $HOME. sudo is available for temporary system changes and diagnostics, but changes made with sudo outside /home/coder can disappear when the workspace is recreated. When a command invokes sudo, this tool returns a structured advisory separately from process output.

The command is executed by the workspace Agent using sh -c. If workdir is omitted, the Agent uses its configured workspace directory or the user's home directory. env values override variables inherited from the Agent environment. background only records background intent in tracked process metadata; Agent-tracked processes are durable regardless of this flag.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "The workspace name in format [owner/]workspace[.agent]. If owner is omitted, the authenticated user is used.",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to start exactly once using sh -c.",
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "Optional working directory. If omitted, the Agent uses its configured workspace directory or the user's home directory.",
				},
				"env": map[string]any{
					"type":                 "object",
					"description":          "Optional environment variable overrides applied on top of the Agent environment.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"background": map[string]any{
					"type":        "boolean",
					"description": "Whether to mark the tracked process as a background command. This does not change process durability.",
				},
			},
			Required: []string{"workspace", "command"},
		},
	},
	MCPAnnotations: mcpMutationAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceProcessStartArgs) (WorkspaceProcessStartResult, error) {
		if args.Workspace == "" {
			return WorkspaceProcessStartResult{}, xerrors.New("workspace name cannot be empty")
		}
		if args.Command == "" {
			return WorkspaceProcessStartResult{}, xerrors.New("command cannot be empty")
		}

		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceProcessStartResult{}, err
		}
		defer conn.Close()

		started, err := conn.StartProcess(ctx, workspacesdk.StartProcessRequest{
			Command:    args.Command,
			WorkDir:    args.WorkDir,
			Env:        args.Env,
			Background: args.Background,
		})
		if err != nil {
			return WorkspaceProcessStartResult{}, xerrors.Errorf("start workspace process: %w", err)
		}
		return WorkspaceProcessStartResult{
			ProcessID:  started.ID,
			Started:    started.Started,
			Advisories: commandAdvisories(args.Command),
		}, nil
	},
}
