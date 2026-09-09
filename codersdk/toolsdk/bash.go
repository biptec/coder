package toolsdk

import (
	"context"
	"io"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/cli/cliui"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

type WorkspaceBashArgs struct {
	Workspace  string `json:"workspace"`
	Command    string `json:"command"`
	TimeoutMs  int    `json:"timeout_ms,omitempty"`
	Background bool   `json:"background,omitempty"`
}

type WorkspaceBashResult struct {
	Output     string         `json:"output"`
	ExitCode   int            `json:"exit_code"`
	Advisories []ToolAdvisory `json:"advisories,omitempty"`
}

var WorkspaceBash = Tool[WorkspaceBashArgs, WorkspaceBashResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceBash,
		Description: `Execute a bash command in a Coder workspace.

Use this convenience tool for short, ordinary commands where repeating the command after a failed request would be safe. For long-running, expensive, side-effectful, or non-idempotent commands, use coder_workspace_process_start instead so the execution can be recovered by process_id after a timeout, 502, or disconnect.

In the standard Developer Workspace, only /home/coder is persistent across workspace recreation. The system filesystem outside /home/coder is ephemeral. Prefer durable tools and dependencies under $HOME. sudo is available for temporary system changes and diagnostics, but changes made with sudo outside /home/coder can disappear when the workspace is recreated. When a command invokes sudo, this tool returns a structured advisory separately from command output; stdout/stderr are not modified.

This tool provides the same functionality as the 'coder ssh <workspace> <command>' CLI command.
It automatically starts the workspace if it's stopped and waits for the agent to be ready.
The output is trimmed of leading and trailing whitespace.

The workspace parameter supports various formats:
- workspace (uses current user)
- owner/workspace
- owner--workspace
- workspace.agent (specific agent)
- owner/workspace.agent

The timeout_ms parameter specifies the command timeout in milliseconds (defaults to 60000ms, maximum of 300000ms).
If the command times out, all output captured up to that point is returned with a cancellation message.

For background commands (background: true), output is captured until the timeout is reached, then the command
continues running in the background. The captured output is returned as the result.

For file operations (list, write, edit), always prefer the dedicated file tools.
Do not use bash commands (ls, cat, echo, heredoc, etc.) to list, write, or read
files when the file tools are available. The bash tool should be used for:

	- Running commands and scripts
	- Installing packages
	- Starting services
	- Executing programs

Examples:
- workspace: "john/dev-env", command: "git status", timeout_ms: 30000
- workspace: "my-workspace", command: "npm run dev", background: true, timeout_ms: 10000
- workspace: "my-workspace.main", command: "docker ps"`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "The workspace name in format [owner/]workspace[.agent]. If owner is not specified, the authenticated user is used.",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "The bash command to execute in the workspace.",
				},
				"timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Command timeout in milliseconds. Defaults to 60000ms (60 seconds) if not specified.",
					"default":     60000,
					"minimum":     1,
				},
				"background": map[string]any{
					"type":        "boolean",
					"description": "Whether to run the command in the background. Output is captured until timeout, then the command continues running in the background.",
				},
			},
			Required: []string{"workspace", "command"},
		},
	},
	MCPAnnotations: mcpDestructiveAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceBashArgs) (res WorkspaceBashResult, err error) {
		if args.Workspace == "" {
			return WorkspaceBashResult{}, xerrors.New("workspace name cannot be empty")
		}
		if args.Command == "" {
			return WorkspaceBashResult{}, xerrors.New("command cannot be empty")
		}

		ctx, cancel := context.WithTimeoutCause(ctx, 5*time.Minute, xerrors.New("MCP handler timeout after 5 min"))
		defer cancel()

		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceBashResult{}, err
		}
		defer conn.Close()

		started, err := conn.StartProcess(ctx, workspacesdk.StartProcessRequest{
			Command:    args.Command,
			Tool:       InvocationToolFromContext(ctx),
			Background: args.Background,
		})
		if err != nil {
			return WorkspaceBashResult{}, xerrors.Errorf("start workspace bash: %w", err)
		}

		timeoutMs := args.TimeoutMs
		if timeoutMs <= 0 {
			timeoutMs = 60_000
		}
		resp, err := waitForWorkspaceProcess(ctx, conn, started.ID, time.Duration(timeoutMs)*time.Millisecond)
		if err != nil {
			return WorkspaceBashResult{}, xerrors.Errorf("wait for workspace bash: %w", err)
		}

		result := workspaceProcessResult(started.ID, resp, commandAdvisories(args.Command))
		output := result.Output
		if resp.Running {
			if args.Background {
				output = strings.TrimSpace(output + "\nCommand continues running in background")
			} else {
				// Bash retains its bounded execution semantics. Agent-tracked processes are
				// durable, so explicitly request termination rather than relying on closing
				// an SSH transport to stop the command.
				_ = conn.SignalProcess(ctx, started.ID, "terminate")
				output = strings.TrimSpace(output + "\nCommand termination requested due to timeout")
			}
		}

		return WorkspaceBashResult{
			Output:     output,
			ExitCode:   result.ExitCode,
			Advisories: result.Advisories,
		}, nil
	},
}

// findWorkspaceAndAgent finds workspace and agent by name with auto-start support
func findWorkspaceAndAgent(ctx context.Context, client *codersdk.Client, workspaceName string) (codersdk.Workspace, codersdk.WorkspaceAgent, error) {
	// Parse workspace name to extract workspace and agent parts
	parts := strings.Split(workspaceName, ".")
	var agentName string
	if len(parts) >= 2 {
		agentName = parts[1]
		workspaceName = parts[0]
	}

	// Get workspace
	workspace, err := client.ResolveWorkspace(ctx, workspaceName)
	if err != nil {
		return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, err
	}

	// Auto-start workspace if needed
	if workspace.LatestBuild.Transition != codersdk.WorkspaceTransitionStart {
		if workspace.LatestBuild.Transition == codersdk.WorkspaceTransitionDelete {
			return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, xerrors.Errorf("workspace %q is deleted", workspace.Name)
		}
		if workspace.LatestBuild.Job.Status == codersdk.ProvisionerJobFailed {
			return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, xerrors.Errorf("workspace %q is in failed state", workspace.Name)
		}
		if workspace.LatestBuild.Status != codersdk.WorkspaceStatusStopped {
			return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, xerrors.Errorf("workspace must be started; was unable to autostart as the last build job is %q, expected %q",
				workspace.LatestBuild.Status, codersdk.WorkspaceStatusStopped)
		}

		// Start workspace
		build, err := client.CreateWorkspaceBuild(ctx, workspace.ID, codersdk.CreateWorkspaceBuildRequest{
			Transition: codersdk.WorkspaceTransitionStart,
		})
		if err != nil {
			return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, xerrors.Errorf("failed to start workspace: %w", err)
		}

		// Wait for build to complete
		if build.Job.CompletedAt == nil {
			err := cliui.WorkspaceBuild(ctx, io.Discard, client, build.ID)
			if err != nil {
				return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, xerrors.Errorf("failed to wait for build completion: %w", err)
			}
		}

		// Refresh workspace after build completes
		workspace, err = client.Workspace(ctx, workspace.ID)
		if err != nil {
			return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, err
		}
	}

	// Find agent
	workspaceAgent, err := getWorkspaceAgent(workspace, agentName)
	if err != nil {
		return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, err
	}

	return workspace, workspaceAgent, nil
}

// getWorkspaceAgent finds the specified agent in the workspace
func getWorkspaceAgent(workspace codersdk.Workspace, agentName string) (codersdk.WorkspaceAgent, error) {
	resources := workspace.LatestBuild.Resources

	var agents []codersdk.WorkspaceAgent
	var availableNames []string

	for _, resource := range resources {
		for _, agent := range resource.Agents {
			availableNames = append(availableNames, agent.Name)
			agents = append(agents, agent)
		}
	}

	if len(agents) == 0 {
		return codersdk.WorkspaceAgent{}, xerrors.Errorf("workspace %q has no agents", workspace.Name)
	}

	if agentName != "" {
		for _, agent := range agents {
			if agent.Name == agentName || agent.ID.String() == agentName {
				return agent, nil
			}
		}
		return codersdk.WorkspaceAgent{}, xerrors.Errorf("agent not found by name %q, available agents: %v", agentName, availableNames)
	}

	if len(agents) == 1 {
		return agents[0], nil
	}

	return codersdk.WorkspaceAgent{}, xerrors.Errorf("multiple agents found, please specify the agent name, available agents: %v", availableNames)
}
