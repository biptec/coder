package toolsdk

import (
	"context"
	"io"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/cli/cliui"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

type WorkspaceBashArgs struct {
	Workspace string `json:"workspace"`
	Command   string `json:"command"`
}

type WorkspaceBashResult struct {
	Output     string                          `json:"output"`
	ExitCode   int                             `json:"exit_code"`
	ProcessID  string                          `json:"process_id,omitempty"`
	Running    bool                            `json:"running,omitempty"`
	Truncated  *workspacesdk.ProcessTruncation `json:"truncated,omitempty"`
	Advisories []ToolAdvisory                  `json:"advisories,omitempty"`
}

var WorkspaceBash = Tool[WorkspaceBashArgs, WorkspaceBashResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceBash,
		Description: `Execute a bash command in a Coder workspace.

Use this convenience tool for short shell commands. Bash has no process execution timeout. One MCP call uses a single shared 60-second observation budget across workspace readiness, process-start acknowledgement, and process observation. If the process finishes within the remaining budget, the tool returns its final output and exit code. If it is still running when the budget is exhausted, the tool returns process_id, running=true, and the latest available output while the same durable process continues independently on the workspace Agent. While running=true, exit_code is only a legacy placeholder and MUST be ignored; it is not the process exit status, timeout, or failure. exit_code is meaningful only when running=false. Continue observing it with coder_workspace_process_output; do not start the command again. If process-start acknowledgement is lost, use coder_workspace_process_list before retrying because the process may already exist.

For commands that are expected to be long-running, expensive, side-effectful, or non-idempotent, prefer coder_workspace_process_start so process_id is returned without waiting for process completion. If shell syntax is not required, prefer coder_workspace_exec.

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

For file operations (list, write, edit), always prefer the dedicated file tools.
Do not use bash commands (ls, cat, echo, heredoc, etc.) to list, write, or read
files when the file tools are available. The bash tool should be used for:

	- Running commands and scripts
	- Installing packages
	- Starting services
	- Executing programs

Examples:
- workspace: "john/dev-env", command: "git status"
- workspace: "my-workspace", command: "npm test"
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

		budget := newMCPObservationBudget()
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return WorkspaceBashResult{}, err
		}
		defer conn.Close()

		started, err := startWorkspaceProcessWithinObservation(ctx, conn, workspacesdk.StartProcessRequest{
			Command: args.Command,
			Tool:    InvocationToolFromContext(ctx),
		}, budget)
		if err != nil {
			return WorkspaceBashResult{}, xerrors.Errorf("start workspace bash: %w", err)
		}

		resp, err := observeWorkspaceProcess(ctx, conn, started.ID, budget)
		if err != nil {
			return WorkspaceBashResult{}, err
		}

		result := workspaceProcessResult(started.ID, resp, commandAdvisories(args.Command))
		bashResult := WorkspaceBashResult{
			Output:     result.Output,
			ExitCode:   result.ExitCode,
			Running:    result.Running,
			Truncated:  result.Truncated,
			Advisories: result.Advisories,
		}
		if result.Running {
			bashResult.ProcessID = result.ProcessID
		}
		return bashResult, nil
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

	// Auto-start workspace if needed. If a previous MCP call already submitted
	// the start and returned at its observation boundary, resume waiting for that
	// same build instead of trying to create another one.
	build := workspace.LatestBuild
	if build.Transition != codersdk.WorkspaceTransitionStart {
		if build.Transition == codersdk.WorkspaceTransitionDelete {
			return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, xerrors.Errorf("workspace %q is deleted", workspace.Name)
		}
		if build.Job.Status == codersdk.ProvisionerJobFailed {
			return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, xerrors.Errorf("workspace %q is in failed state", workspace.Name)
		}
		if build.Status != codersdk.WorkspaceStatusStopped {
			return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, xerrors.Errorf("workspace must be started; was unable to autostart as the last build job is %q, expected %q",
				build.Status, codersdk.WorkspaceStatusStopped)
		}

		var err error
		build, err = client.CreateWorkspaceBuild(ctx, workspace.ID, codersdk.CreateWorkspaceBuildRequest{
			Transition: codersdk.WorkspaceTransitionStart,
		})
		if err != nil {
			return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, xerrors.Errorf("failed to start workspace: %w", err)
		}
	}

	if build.Job.Status == codersdk.ProvisionerJobFailed {
		return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, xerrors.Errorf("workspace %q start build %s is in failed state", workspace.Name, build.ID)
	}

	if build.Job.CompletedAt == nil {
		if err := cliui.WorkspaceBuild(ctx, io.Discard, client, build.ID); err != nil {
			return codersdk.Workspace{}, codersdk.WorkspaceAgent{}, xerrors.Errorf("failed to wait for workspace build %s completion: %w", build.ID, err)
		}

		// Refresh workspace after the newly-created or already-running start build.
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
