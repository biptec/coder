package toolsdk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coder/aisdk-go"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const (
	defaultWorkspaceProcessWait = 10 * time.Second
	maxWorkspaceProcessWait     = 60 * time.Second
	processSnapshotTimeout      = 5 * time.Second
)

type WorkspaceProcessStartArgs struct {
	Workspace  string            `json:"workspace"`
	Command    string            `json:"command"`
	WorkDir    string            `json:"workdir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Background bool              `json:"background,omitempty"`
}

type WorkspaceProcessStartResult struct {
	ProcessID  string         `json:"process_id"`
	Started    bool           `json:"started"`
	Advisories []ToolAdvisory `json:"advisories,omitempty"`
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

// WorkspaceProcessResult is the state returned for a tracked workspace process.
type WorkspaceProcessResult struct {
	Output     string                          `json:"output"`
	ExitCode   int                             `json:"exit_code"`
	ProcessID  string                          `json:"process_id"`
	Running    bool                            `json:"running"`
	Truncated  *workspacesdk.ProcessTruncation `json:"truncated,omitempty"`
	Advisories []ToolAdvisory                  `json:"advisories,omitempty"`
}

type WorkspaceProcessOutputArgs struct {
	Workspace     string `json:"workspace"`
	ProcessID     string `json:"process_id"`
	WaitTimeoutMs *int   `json:"wait_timeout_ms,omitempty"`
}

var WorkspaceProcessOutput = Tool[WorkspaceProcessOutputArgs, WorkspaceProcessResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceProcessOutput,
		Description: `Read output from a durable process previously started in a Coder workspace.

Use the process_id returned by coder_workspace_process_start or coder_workspace_process_list. This tool can wait briefly for more output or process exit, then returns the current state. The process itself is independent of this request and keeps running if this tool call disconnects.

After any timeout, 502, reconnect, or uncertain result, use coder_workspace_process_list and this tool to recover the existing process before considering another command execution. If the recovered process command invokes sudo, this tool also returns the same structured persistence advisory separately from process output.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "The workspace name in format [owner/]workspace[.agent]. If owner is omitted, the authenticated user is used.",
				},
				"process_id": map[string]any{
					"type":        "string",
					"description": "Tracked process ID returned by coder_workspace_process_start or coder_workspace_process_list.",
				},
				"wait_timeout_ms": map[string]any{
					"type":        "integer",
					"description": "How long this observation call may wait for process exit or more output. Defaults to 10000ms. Use 0 for an immediate snapshot. Maximum 60000ms.",
					"default":     10000,
					"minimum":     0,
					"maximum":     60000,
				},
			},
			Required: []string{"workspace", "process_id"},
		},
	},
	MCPAnnotations: mcpReadOnlyAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceProcessOutputArgs) (WorkspaceProcessResult, error) {
		if args.Workspace == "" {
			return WorkspaceProcessResult{}, xerrors.New("workspace name cannot be empty")
		}
		if args.ProcessID == "" {
			return WorkspaceProcessResult{}, xerrors.New("process_id cannot be empty")
		}

		wait, err := workspaceProcessWaitDuration(args.WaitTimeoutMs)
		if err != nil {
			return WorkspaceProcessResult{}, err
		}

		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceProcessResult{}, err
		}
		defer conn.Close()

		resp, err := waitForWorkspaceProcess(ctx, conn, args.ProcessID, wait)
		if err != nil {
			return WorkspaceProcessResult{}, err
		}
		advisories := workspaceProcessAdvisories(ctx, conn, args.ProcessID)
		return workspaceProcessResult(args.ProcessID, resp, advisories), nil
	},
}

type WorkspaceProcessListArgs struct {
	Workspace string `json:"workspace"`
}

type WorkspaceProcessInfo struct {
	workspacesdk.ProcessInfo
	Advisories []ToolAdvisory `json:"advisories,omitempty"`
}

type WorkspaceProcessListResult struct {
	Processes []WorkspaceProcessInfo `json:"processes"`
}

var WorkspaceProcessList = Tool[WorkspaceProcessListArgs, WorkspaceProcessListResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceProcessList,
		Description: `List durable processes tracked by a Coder workspace agent.

Use this after a timeout, 502, reconnect, or any uncertain command result before running the command again. It lets you recover the original process_id and inspect whether the command is still running or already exited. Processes whose command invokes sudo include a structured persistence advisory alongside their metadata.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "The workspace name in format [owner/]workspace[.agent]. If owner is omitted, the authenticated user is used.",
				},
			},
			Required: []string{"workspace"},
		},
	},
	MCPAnnotations: mcpReadOnlyAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceProcessListArgs) (WorkspaceProcessListResult, error) {
		if args.Workspace == "" {
			return WorkspaceProcessListResult{}, xerrors.New("workspace name cannot be empty")
		}

		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceProcessListResult{}, err
		}
		defer conn.Close()

		resp, err := conn.ListProcesses(ctx)
		if err != nil {
			return WorkspaceProcessListResult{}, xerrors.Errorf("list workspace processes: %w", err)
		}
		processes := make([]WorkspaceProcessInfo, 0, len(resp.Processes))
		for _, process := range resp.Processes {
			processes = append(processes, WorkspaceProcessInfo{
				ProcessInfo: process,
				Advisories:  commandAdvisories(process.Command),
			})
		}
		return WorkspaceProcessListResult{Processes: processes}, nil
	},
}

type WorkspaceProcessSignalArgs struct {
	Workspace string `json:"workspace"`
	ProcessID string `json:"process_id"`
	Signal    string `json:"signal"`
}

type WorkspaceProcessSignalResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

var WorkspaceProcessSignal = Tool[WorkspaceProcessSignalArgs, WorkspaceProcessSignalResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceProcessSignal,
		Description: `Send a signal to a durable process tracked by a Coder workspace agent.

Use signal "terminate" for graceful shutdown or "kill" to force stop. Always identify the intended process with coder_workspace_process_list first when there is any ambiguity.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "The workspace name in format [owner/]workspace[.agent]. If owner is omitted, the authenticated user is used.",
				},
				"process_id": map[string]any{
					"type":        "string",
					"description": "Tracked process ID.",
				},
				"signal": map[string]any{
					"type":        "string",
					"description": "Signal to send.",
					"enum":        []string{"terminate", "kill"},
				},
			},
			Required: []string{"workspace", "process_id", "signal"},
		},
	},
	MCPAnnotations: mcpDestructiveAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceProcessSignalArgs) (WorkspaceProcessSignalResult, error) {
		if args.Workspace == "" {
			return WorkspaceProcessSignalResult{}, xerrors.New("workspace name cannot be empty")
		}
		if args.ProcessID == "" {
			return WorkspaceProcessSignalResult{}, xerrors.New("process_id cannot be empty")
		}
		if args.Signal != "terminate" && args.Signal != "kill" {
			return WorkspaceProcessSignalResult{}, xerrors.New(`signal must be "terminate" or "kill"`)
		}

		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceProcessSignalResult{}, err
		}
		defer conn.Close()

		if err := conn.SignalProcess(ctx, args.ProcessID, args.Signal); err != nil {
			return WorkspaceProcessSignalResult{}, xerrors.Errorf("signal workspace process: %w", err)
		}
		return WorkspaceProcessSignalResult{
			Success: true,
			Message: fmt.Sprintf("signal %q sent to process %s", args.Signal, args.ProcessID),
		}, nil
	},
}

func workspaceProcessWaitDuration(waitTimeoutMs *int) (time.Duration, error) {
	if waitTimeoutMs == nil {
		return defaultWorkspaceProcessWait, nil
	}
	if *waitTimeoutMs < 0 {
		return 0, xerrors.New("wait_timeout_ms cannot be negative")
	}
	wait := time.Duration(*waitTimeoutMs) * time.Millisecond
	if wait > maxWorkspaceProcessWait {
		return 0, xerrors.Errorf("wait_timeout_ms cannot exceed %d", maxWorkspaceProcessWait.Milliseconds())
	}
	return wait, nil
}

func waitForWorkspaceProcess(
	ctx context.Context,
	conn workspacesdk.AgentConn,
	processID string,
	wait time.Duration,
) (workspacesdk.ProcessOutputResponse, error) {
	if wait <= 0 {
		resp, err := conn.ProcessOutput(ctx, processID, nil)
		if err != nil {
			return workspacesdk.ProcessOutputResponse{}, xerrors.Errorf("get process output: %w", err)
		}
		return resp, nil
	}

	parentCtx := ctx
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	resp, err := conn.ProcessOutput(waitCtx, processID, &workspacesdk.ProcessOutputOptions{Wait: true})
	cancel()
	if err == nil {
		return resp, nil
	}

	// If the caller itself went away, the tracked process still survives on the
	// agent. There is no useful response channel left, so let the caller retry
	// with process_list/process_output on a new request.
	if parentCtx.Err() != nil {
		return workspacesdk.ProcessOutputResponse{}, err
	}

	// A local observation timeout (or a transient transport failure) must not be
	// interpreted as process failure. Recover a non-blocking snapshot using the
	// still-live parent request.
	snapshotCtx, snapshotCancel := context.WithTimeout(parentCtx, processSnapshotTimeout)
	defer snapshotCancel()
	snapshot, snapshotErr := conn.ProcessOutput(snapshotCtx, processID, nil)
	if snapshotErr == nil {
		return snapshot, nil
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return workspacesdk.ProcessOutputResponse{}, xerrors.Errorf("process observation timed out and snapshot failed: %w", snapshotErr)
	}
	return workspacesdk.ProcessOutputResponse{}, xerrors.Errorf("get process output: %v; snapshot failed: %w", err, snapshotErr)
}

func workspaceProcessAdvisories(ctx context.Context, conn workspacesdk.AgentConn, processID string) []ToolAdvisory {
	resp, err := conn.ListProcesses(ctx)
	if err != nil {
		return nil
	}
	for _, process := range resp.Processes {
		if process.ID == processID {
			return commandAdvisories(process.Command)
		}
	}
	return nil
}

func workspaceProcessResult(processID string, resp workspacesdk.ProcessOutputResponse, advisorySets ...[]ToolAdvisory) WorkspaceProcessResult {
	var advisories []ToolAdvisory
	if len(advisorySets) > 0 {
		advisories = advisorySets[0]
	}

	exitCode := 124
	if !resp.Running {
		exitCode = 0
		if resp.ExitCode != nil {
			exitCode = *resp.ExitCode
		}
	}
	return WorkspaceProcessResult{
		Output:     strings.TrimSpace(resp.Output),
		ExitCode:   exitCode,
		ProcessID:  processID,
		Running:    resp.Running,
		Truncated:  resp.Truncated,
		Advisories: advisories,
	}
}
