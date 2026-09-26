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
	Workspace      string               `json:"workspace"`
	Command        string               `json:"command"`
	WorkDir        string               `json:"workdir,omitempty"`
	Env            map[string]string    `json:"env,omitempty"`
	Interactive    bool                 `json:"interactive,omitempty"`
	Stdin          string               `json:"stdin,omitempty"`
	SSH            *WorkspaceSSHOptions `json:"ssh,omitempty"`
	WaitTimeoutMs  *int                 `json:"wait_timeout_ms,omitempty"`
	AllowDuplicate bool                 `json:"allow_duplicate,omitempty"`
}

var WorkspaceBash = Tool[WorkspaceBashArgs, WorkspaceProcessResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceBash,
		Description: `Run an intentional POSIX shell command with sh -c as a durable tracked process when pipes, redirects, or expansion are required; do not substitute it for semantic/file tools.

Use this tool only when shell syntax such as pipes, redirects, &&, loops,
substitutions, or expansion is intentional. For direct executable argv without
shell parsing, use start_process. Do not use shell cat/grep/sed/awk commands as
a substitute for dedicated semantic navigation or file read/edit tools when
those tools can express the operation directly.

After start acknowledgement this call observes initial output only when a positive
wait_timeout_ms is requested. Omit it or use 0 for an immediate snapshot.
Observation time never limits process lifetime. The response always includes
process_id and current process state.

Set interactive=true only when later interact_with_process calls are required.
Set ssh to execute on a remote host through the workspace OpenSSH client.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": "The workspace name in format [owner/]workspace[.agent]."},
				"command":   map[string]any{"type": "string", "description": "Shell command executed with explicit sh -c semantics."},
				"workdir":   map[string]any{"type": "string", "description": "Optional working directory."},
				"env": map[string]any{
					"type": "object", "description": "Optional environment variable overrides.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"interactive": map[string]any{"type": "boolean", "description": "Keep stdin open for later interact_with_process calls. Defaults to false."},
				"stdin": map[string]any{
					"type": "string", "description": "Optional initial stdin. Non-interactive mode sends EOF after this content; interactive mode keeps stdin open.",
				},
				"ssh":             workspaceSSHSchema(),
				"allow_duplicate": map[string]any{"type": "boolean", "description": "Start a second identical concurrently running shell process. Defaults to false; normally an identical running process is reused instead."},
				"wait_timeout_ms": map[string]any{
					"type": "integer", "description": "Optional initial output observation interval in milliseconds. Omit or use 0 for an immediate snapshot. This never limits process lifetime.",
					"minimum": 0,
				},
			},
			Required: []string{"workspace", "command"},
		},
	},
	MCPAnnotations: mcpExecutionAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceBashArgs) (WorkspaceProcessResult, error) {
		if args.Workspace == "" {
			return WorkspaceProcessResult{}, xerrors.New("workspace name cannot be empty")
		}
		if args.Command == "" {
			return WorkspaceProcessResult{}, xerrors.New("command cannot be empty")
		}
		wait, err := workspaceInitialProcessWaitDuration(args.WaitTimeoutMs, deps.MCPToolTimeoutMax())
		if err != nil {
			return WorkspaceProcessResult{}, err
		}
		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return WorkspaceProcessResult{}, err
		}
		defer conn.Close()
		applyInvocationScopeHeader(ctx, conn)

		request := workspacesdk.StartProcessRequest{
			Command:     args.Command,
			WorkDir:     args.WorkDir,
			Env:         args.Env,
			Tool:        InvocationToolFromContext(ctx),
			Interactive: args.Interactive,
			Stdin:       args.Stdin,
		}
		if err := applyWorkspaceSSHOptions(&request, args.SSH); err != nil {
			return WorkspaceProcessResult{}, err
		}
		fingerprint, err := processLaunchFingerprint(request)
		if err != nil {
			return WorkspaceProcessResult{}, xerrors.Errorf("fingerprint workspace shell launch: %w", err)
		}
		request.Fingerprint = fingerprint
		request.AllowDuplicate = args.AllowDuplicate
		advisories := commandAdvisories(args.Command)
		started, err := startWorkspaceProcessWithinObservation(ctx, conn, request, budget)
		if err != nil {
			return WorkspaceProcessResult{}, xerrors.Errorf("start workspace shell command: %w", err)
		}
		resp, observeErr := observeInitialWorkspaceProcess(ctx, conn, started.ID, wait, budget)
		if observeErr != nil {
			return WorkspaceProcessResult{ProcessID: started.ID, Running: true, Advisories: advisories, DuplicateReused: !started.Started}, nil
		}
		result := workspaceProcessResult(started.ID, resp, advisories)
		result.DuplicateReused = !started.Started
		next := resp.NextCursor
		result.NextCursor = &next
		return result, nil
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
