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

type WorkspaceBashResult = WorkspaceProcessResult

var WorkspaceBash = Tool[WorkspaceBashArgs, WorkspaceBashResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceBash,
		Description: `Execute a bash command in a Coder workspace.

This tool provides the same functionality as the 'coder ssh <workspace> <command>' CLI command.
It automatically starts the workspace if it's stopped and waits for the agent to be ready.
The output is trimmed of leading and trailing whitespace.

The workspace parameter supports various formats:
- workspace (uses current user)
- owner/workspace
- owner--workspace
- workspace.agent (specific agent)
- owner/workspace.agent

The timeout_ms parameter specifies how long this tool call observes the command before returning (defaults to 60000ms, maximum 60000ms).
Every command is started exactly once as a durable Agent-tracked process. If it is still running when the observation timeout is reached, the result includes process_id and running=true; DO NOT run the command again. Use coder_workspace_process_output to keep watching the same process.

If a tool call disconnects or returns 502 after the process has started, the process survives. Use coder_workspace_process_list first to recover the existing process before considering another execution. For background commands (background: true), the process uses the same durable tracking and can be monitored with the process tools.

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
					"description": "How long this tool call observes the durable process before returning. Defaults to 60000ms (60 seconds). Maximum 60000ms; longer commands continue and are monitored by process_id.",
					"default":     60000,
					"minimum":     1,
					"maximum":     60000,
				},
				"background": map[string]any{
					"type":        "boolean",
					"description": "Mark the durable tracked process as a background command. It is still monitored by process_id and survives request disconnects.",
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

		// timeout_ms now bounds only this observation request, never the process lifetime.
		timeoutMs := args.TimeoutMs
		if timeoutMs <= 0 {
			timeoutMs = 60000
		}
		if timeoutMs > int(maxWorkspaceProcessWait.Milliseconds()) {
			return WorkspaceBashResult{}, xerrors.Errorf("timeout_ms cannot exceed %d", maxWorkspaceProcessWait.Milliseconds())
		}

		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceBashResult{}, err
		}
		defer conn.Close()

		// The agent process manager owns the process independently of this HTTP/MCP
		// request. If the request is canceled after StartProcess succeeds, the command
		// keeps running and can be recovered with process_list/process_output.
		started, err := conn.StartProcess(ctx, workspacesdk.StartProcessRequest{
			Command:    workspaceBashCommand(args.Command),
			Background: args.Background,
		})
		if err != nil {
			return WorkspaceBashResult{}, xerrors.Errorf("start workspace process: %w", err)
		}

		resp, err := waitForWorkspaceProcess(ctx, conn, started.ID, time.Duration(timeoutMs)*time.Millisecond)
		if err != nil {
			return WorkspaceBashResult{}, err
		}
		result := workspaceProcessResult(started.ID, resp)
		if result.Running && args.Background {
			if result.Output != "" {
				result.Output += "\n"
			}
			result.Output += "Command continues running in background"
		}
		return result, nil
	},
}

func workspaceBashCommand(command string) string {
	return "exec bash -lc " + shellSingleQuote(command)
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
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
