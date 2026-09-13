package toolsdk

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const processSnapshotTimeout = 5 * time.Second

type mcpObservationBudget struct {
	deadline time.Time
	window   time.Duration
	max      time.Duration
}

func newMCPObservationBudget(deps Deps) mcpObservationBudget {
	maxTimeout := deps.MCPToolTimeoutMax()
	window := maxTimeout
	// Reserve a short tail for a final non-blocking snapshot and response
	// serialization. The configured maximum still bounds the whole MCP call.
	if window > processSnapshotTimeout {
		window -= processSnapshotTimeout
	}
	return mcpObservationBudget{
		deadline: time.Now().Add(window),
		window:   window,
		max:      maxTimeout,
	}
}

func (b mcpObservationBudget) context(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithDeadline(ctx, b.deadline)
}

func (b mcpObservationBudget) remaining() time.Duration {
	remaining := time.Until(b.deadline)
	if remaining < 0 {
		return 0
	}
	return remaining
}

type WorkspaceProcessStartV2Args struct {
	Workspace    string            `json:"workspace"`
	Command      string            `json:"command,omitempty"`
	Argv         []string          `json:"argv,omitempty"`
	WorkDir      string            `json:"workdir,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Host         string            `json:"host,omitempty"`
	IdentityFile string            `json:"identity_file,omitempty"`
	Background   bool              `json:"background,omitempty"`
	Interactive  bool              `json:"interactive,omitempty"`
	Stdin        string            `json:"stdin,omitempty"`
}

type WorkspaceProcessStartResult struct {
	ProcessID  string         `json:"process_id"`
	Started    bool           `json:"started"`
	Advisories []ToolAdvisory `json:"advisories,omitempty"`
}

var WorkspaceProcessStartV2 = Tool[WorkspaceProcessStartV2Args, WorkspaceProcessStartResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceProcessStartV2,
		Description: `Start a durable process in a Coder workspace and return its process_id as soon as the workspace Agent acknowledges the start.

Use this tool instead of coder_workspace_bash or coder_workspace_exec when a command may run for a long time, is expensive, has side effects, or must not be executed twice. This tool starts the command exactly once and does not wait for completion or return command output. After a successful start, use coder_workspace_process_output with the returned process_id to observe the same process.

A single MCP call is bounded by the deployment-wide MCP tool timeout across workspace auto-start/build readiness and process-start acknowledgement. If that budget elapses before process submission, any workspace start/build already submitted continues independently and the process itself was not submitted; retry process_start after the workspace is ready. If acknowledgement is lost after submission, the process may already exist: use process_list before retrying. Once process submission is acknowledged, process_id is returned without waiting for process completion, and the process lifetime is independent of the MCP connection.

Exactly one execution mode is required: argv for direct execution without shell parsing, or command for intentional sh -c shell syntax. Prefer argv. Set interactive=true only when later process_input calls are required.

If this call ends with a timeout, 502, disconnect, or any other uncertain result after process submission, DO NOT start the command again. First call coder_workspace_process_list for the same workspace and recover the existing process by matching its command, workdir, and start time.

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
					"description": "Optional shell command executed with sh -c. Mutually exclusive with argv.",
				},
				"argv": map[string]any{
					"type":        "array",
					"description": "Optional executable and arguments for direct execution without shell parsing. Mutually exclusive with command.",
					"minItems":    1,
					"items":       map[string]any{"type": "string"},
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "Optional working directory. If omitted, the Agent uses its configured workspace directory or the user's home directory.",
				},
				"env": map[string]any{
					"type":                 "object",
					"description":          "Optional environment variable overrides. For remote execution they are applied to the remote command.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"host": map[string]any{
					"type":        "string",
					"description": "Optional SSH alias returned by remote_hosts. Omit for local workspace execution.",
				},
				"background": map[string]any{
					"type":        "boolean",
					"description": "Whether to mark the tracked process as a background command. This does not change process durability.",
				},
				"interactive": map[string]any{
					"type":        "boolean",
					"description": "Keep stdin open for later process_input calls. Defaults to false.",
				},
				"stdin": map[string]any{
					"type":        "string",
					"description": "Optional initial stdin. Non-interactive mode sends EOF after this content; interactive mode keeps stdin open. Maximum 1 MiB.",
					"maxLength":   workspacesdk.MaxProcessInputBytes,
				},
			},
			Required: []string{"workspace"},
		},
	},
	MCPAnnotations: mcpMutationOpenWorldAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceProcessStartV2Args) (WorkspaceProcessStartResult, error) {
		if args.Workspace == "" {
			return WorkspaceProcessStartResult{}, xerrors.New("workspace name cannot be empty")
		}
		if args.Command == "" && len(args.Argv) == 0 {
			return WorkspaceProcessStartResult{}, xerrors.New("command cannot be empty; alternatively provide argv")
		}
		if args.Command != "" && len(args.Argv) != 0 {
			return WorkspaceProcessStartResult{}, xerrors.New("command and argv are mutually exclusive")
		}
		if len(args.Argv) > 0 && args.Argv[0] == "" {
			return WorkspaceProcessStartResult{}, xerrors.New("argv[0] must not be empty")
		}
		if len(args.Stdin) > workspacesdk.MaxProcessInputBytes {
			return WorkspaceProcessStartResult{}, xerrors.Errorf("stdin cannot exceed %d bytes", workspacesdk.MaxProcessInputBytes)
		}
		if err := validateRemoteTarget(args.Host, args.IdentityFile); err != nil {
			return WorkspaceProcessStartResult{}, err
		}

		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return WorkspaceProcessStartResult{}, err
		}
		defer conn.Close()

		started, err := startWorkspaceProcessWithinObservation(ctx, conn, workspacesdk.StartProcessRequest{
			Command:      args.Command,
			Argv:         args.Argv,
			WorkDir:      args.WorkDir,
			Env:          args.Env,
			Host:         args.Host,
			IdentityFile: args.IdentityFile,
			Tool:         InvocationToolFromContext(ctx),
			Background:   args.Background,
			Interactive:  args.Interactive,
			Stdin:        args.Stdin,
		}, budget)
		if err != nil {
			return WorkspaceProcessStartResult{}, xerrors.Errorf("start workspace process: %w", err)
		}
		advisories := commandAdvisories(args.Command)
		if len(args.Argv) > 0 {
			advisories = argvAdvisories(args.Argv)
		}
		return WorkspaceProcessStartResult{
			ProcessID:  started.ID,
			Started:    started.Started,
			Advisories: advisories,
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
	NextCursor *int64                          `json:"next_cursor,omitempty"`
	GapBytes   int64                           `json:"gap_bytes,omitempty"`
	HasMore    bool                            `json:"has_more,omitempty"`
	Advisories []ToolAdvisory                  `json:"advisories,omitempty"`
}

type WorkspaceProcessOutputArgs struct {
	Workspace     string `json:"workspace"`
	ProcessID     string `json:"process_id"`
	WaitTimeoutMs *int   `json:"wait_timeout_ms,omitempty"`
	Cursor        *int64 `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

var WorkspaceProcessOutput = Tool[WorkspaceProcessOutputArgs, WorkspaceProcessResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceProcessOutput,
		Description: `Read output from a durable process previously started in a Coder workspace.

Use the process_id returned by coder_workspace_process_start or coder_workspace_process_list. Pass cursor=0 to use incremental output; subsequent calls should pass next_cursor. Incremental output is backed by a bounded rolling buffer: if the caller falls behind, gap_bytes reports evicted bytes. Omit cursor for the legacy head+tail snapshot.

This tool is observation-only. Without wait_timeout_ms it returns an immediate snapshot. When wait_timeout_ms is provided, it waits up to that interval for new output or process exit, bounded by the deployment-wide MCP tool timeout. Reaching an observation limit never terminates the durable process; the tool returns the current snapshot with running=true and the caller can invoke process_output again. While running=true, exit_code is only a legacy placeholder and MUST NOT be interpreted as the process exit status, timeout, or failure; exit_code is meaningful only when running=false.

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
					"description": "Optional output-wait interval in milliseconds. Omit it (or use 0) for an immediate snapshot. The deployment-wide MCP tool timeout is the maximum. This never limits the process lifetime.",
					"minimum":     0,
				},
				"cursor": map[string]any{
					"type":        "integer",
					"description": "Optional absolute byte cursor for incremental output. Start with 0 and continue with next_cursor.",
					"minimum":     0,
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum incremental bytes to return. Defaults to 32768 and cannot exceed 32768.",
					"minimum":     1,
					"maximum":     32768,
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

		wait, err := workspaceProcessWaitDuration(args.WaitTimeoutMs, deps.MCPToolTimeoutMax())
		if err != nil {
			return WorkspaceProcessResult{}, err
		}

		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return WorkspaceProcessResult{}, err
		}
		defer conn.Close()

		if args.Cursor != nil && *args.Cursor < 0 {
			return WorkspaceProcessResult{}, xerrors.New("cursor cannot be negative")
		}
		if args.Limit < 0 || args.Limit > 32768 {
			return WorkspaceProcessResult{}, xerrors.New("limit cannot exceed 32768")
		}

		operationCtx, cancel := budget.context(ctx)
		defer cancel()
		wait = workspaceProcessWaitWithinBudget(wait, budget)
		resp, err := waitForWorkspaceProcessOptions(operationCtx, conn, args.ProcessID, wait, args.Cursor, args.Limit)
		if err != nil {
			return WorkspaceProcessResult{}, err
		}
		advisories := workspaceProcessAdvisories(operationCtx, conn, args.ProcessID)
		result := workspaceProcessResult(args.ProcessID, resp, advisories)
		if args.Cursor != nil {
			result.Output = resp.Output
			next := resp.NextCursor
			result.NextCursor = &next
		}
		return result, nil
	},
}

type WorkspaceProcessListArgs struct {
	Workspace string `json:"workspace"`
	Host      string `json:"host,omitempty"`
	Cursor    int    `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type WorkspaceProcessInfo struct {
	workspacesdk.ProcessInfo
	Advisories []ToolAdvisory `json:"advisories,omitempty"`
}

type WorkspaceProcessListResult struct {
	Processes  []WorkspaceProcessInfo `json:"processes"`
	NextCursor *int                   `json:"next_cursor,omitempty"`
}

var WorkspaceProcessList = Tool[WorkspaceProcessListArgs, WorkspaceProcessListResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceProcessList,
		Description: `List durable processes tracked by a Coder workspace agent.

Use this after a timeout, 502, reconnect, or any uncertain command result before running the command again. By default it lists both local and remote tracked processes; use host to filter remote processes for one SSH target. It lets you recover the original process_id and inspect whether the command is still running or already exited. Processes whose command invokes sudo include a structured persistence advisory alongside their metadata.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "The workspace name in format [owner/]workspace[.agent]. If owner is omitted, the authenticated user is used.",
				},
				"host": map[string]any{
					"type":        "string",
					"description": "Optional exact SSH target filter. Omit to list both local and remote tracked processes.",
				},
				"cursor": map[string]any{
					"type":        "integer",
					"description": "Zero-based result cursor. Defaults to 0.",
					"minimum":     0,
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum processes to return. Defaults to 10, maximum 100.",
					"minimum":     1,
					"maximum":     100,
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

		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return WorkspaceProcessListResult{}, err
		}
		defer conn.Close()

		if args.Cursor < 0 {
			return WorkspaceProcessListResult{}, xerrors.New("cursor cannot be negative")
		}
		limit := args.Limit
		if limit == 0 {
			limit = 10
		}
		if limit < 1 || limit > 100 {
			return WorkspaceProcessListResult{}, xerrors.New("limit must be between 1 and 100")
		}
		operationCtx, cancel := budget.context(ctx)
		defer cancel()
		resp, err := conn.ListProcesses(operationCtx)
		if err != nil {
			return WorkspaceProcessListResult{}, xerrors.Errorf("list workspace processes: %w", err)
		}
		filtered := resp.Processes
		if args.Host != "" {
			filtered = make([]workspacesdk.ProcessInfo, 0, len(resp.Processes))
			for _, process := range resp.Processes {
				if strings.EqualFold(process.Host, strings.TrimSpace(args.Host)) {
					filtered = append(filtered, process)
				}
			}
		}
		start := args.Cursor
		if start > len(filtered) {
			start = len(filtered)
		}
		end := start + limit
		if end > len(filtered) {
			end = len(filtered)
		}
		processes := make([]WorkspaceProcessInfo, 0, end-start)
		for _, process := range filtered[start:end] {
			advisories := commandAdvisories(process.Command)
			if len(process.Argv) > 0 {
				advisories = argvAdvisories(process.Argv)
			}
			processes = append(processes, WorkspaceProcessInfo{
				ProcessInfo: process,
				Advisories:  advisories,
			})
		}
		var next *int
		if end < len(filtered) {
			value := end
			next = &value
		}
		return WorkspaceProcessListResult{Processes: processes, NextCursor: next}, nil
	},
}

type WorkspaceProcessInputArgs struct {
	Workspace string `json:"workspace"`
	ProcessID string `json:"process_id"`
	Data      string `json:"data,omitempty"`
	Close     bool   `json:"close,omitempty"`
}

type WorkspaceProcessInputResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

var WorkspaceProcessInput = Tool[WorkspaceProcessInputArgs, WorkspaceProcessInputResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceProcessInput,
		Description: `Write to stdin of a durable process started with interactive=true.

Use close=true to send EOF after optional data. This tool only targets processes
tracked by the workspace agent and preserves chat/process isolation.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "The workspace name in format [owner/]workspace[.agent].",
				},
				"process_id": map[string]any{
					"type":        "string",
					"description": "Tracked process ID returned by process_start.",
				},
				"data": map[string]any{
					"type":        "string",
					"description": "Data to write verbatim to process stdin. Maximum 1 MiB per call.",
					"maxLength":   workspacesdk.MaxProcessInputBytes,
				},
				"close": map[string]any{
					"type":        "boolean",
					"description": "Close stdin after writing data, sending EOF.",
				},
			},
			Required: []string{"workspace", "process_id"},
		},
	},
	MCPAnnotations: mcpMutationOpenWorldAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceProcessInputArgs) (WorkspaceProcessInputResult, error) {
		if args.Workspace == "" {
			return WorkspaceProcessInputResult{}, xerrors.New("workspace name cannot be empty")
		}
		if args.ProcessID == "" {
			return WorkspaceProcessInputResult{}, xerrors.New("process_id cannot be empty")
		}
		if args.Data == "" && !args.Close {
			return WorkspaceProcessInputResult{}, xerrors.New("data must be non-empty or close must be true")
		}
		if len(args.Data) > workspacesdk.MaxProcessInputBytes {
			return WorkspaceProcessInputResult{}, xerrors.Errorf("data cannot exceed %d bytes", workspacesdk.MaxProcessInputBytes)
		}
		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return WorkspaceProcessInputResult{}, err
		}
		defer conn.Close()
		operationCtx, cancel := budget.context(ctx)
		defer cancel()
		if err := conn.ProcessInput(operationCtx, args.ProcessID, workspacesdk.ProcessInputRequest{Data: args.Data, Close: args.Close}); err != nil {
			return WorkspaceProcessInputResult{}, xerrors.Errorf("send workspace process input: %w", err)
		}
		return WorkspaceProcessInputResult{Success: true, Message: "process input sent"}, nil
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
	MCPAnnotations: mcpDestructiveOpenWorldAnnotations,
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

		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return WorkspaceProcessSignalResult{}, err
		}
		defer conn.Close()

		operationCtx, cancel := budget.context(ctx)
		defer cancel()
		if err := conn.SignalProcess(operationCtx, args.ProcessID, args.Signal); err != nil {
			return WorkspaceProcessSignalResult{}, xerrors.Errorf("signal workspace process: %w", err)
		}
		return WorkspaceProcessSignalResult{
			Success: true,
			Message: fmt.Sprintf("signal %q sent to process %s", args.Signal, args.ProcessID),
		}, nil
	},
}

func workspaceProcessWaitWithinBudget(wait time.Duration, budget mcpObservationBudget) time.Duration {
	remaining := budget.remaining()
	if remaining <= 0 {
		return 0
	}
	if wait > remaining {
		return remaining
	}
	return wait
}

func workspaceProcessWaitDuration(waitTimeoutMs *int, maxWait time.Duration) (time.Duration, error) {
	if waitTimeoutMs == nil {
		return 0, nil
	}
	if *waitTimeoutMs < 0 {
		return 0, xerrors.New("wait_timeout_ms cannot be negative")
	}
	wait := time.Duration(*waitTimeoutMs) * time.Millisecond
	if wait > maxWait {
		return 0, xerrors.Errorf("wait_timeout_ms cannot exceed %d", maxWait.Milliseconds())
	}
	return wait, nil
}

func workspaceExecutionWaitDuration(waitTimeoutMs *int, maxWait time.Duration) (time.Duration, error) {
	if waitTimeoutMs == nil {
		return maxWait, nil
	}
	return workspaceProcessWaitDuration(waitTimeoutMs, maxWait)
}

// observeWorkspaceProcess keeps a short MCP request open while a tracked
// process runs. The observation deadline never controls the process lifetime:
// when it expires, the process stays registered on the workspace Agent and we
// return a non-blocking snapshot so the caller can continue with process_output.
func observeWorkspaceProcess(
	ctx context.Context,
	conn workspacesdk.AgentConn,
	processID string,
	budget mcpObservationBudget,
	wait time.Duration,
) (workspacesdk.ProcessOutputResponse, error) {
	// If readiness and StartProcess already consumed the observation budget, or
	// the caller explicitly requested no wait, return a current snapshot.
	last := workspacesdk.ProcessOutputResponse{Running: true}
	remaining := budget.remaining()
	if remaining <= 0 {
		return last, nil
	}
	if wait <= 0 {
		return workspaceProcessSnapshot(ctx, conn, processID, nil, 0)
	}
	if wait > remaining {
		wait = remaining
	}

	observationCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	for {
		resp, err := conn.ProcessOutput(observationCtx, processID, &workspacesdk.ProcessOutputOptions{Wait: true})
		if err == nil {
			last = resp
			if !resp.Running {
				return resp, nil
			}
			// ProcessOutput wakes on output as well as exit. Keep the latest
			// snapshot and continue until the shared MCP budget is exhausted.
			continue
		}

		if ctx.Err() != nil {
			return workspacesdk.ProcessOutputResponse{}, err
		}
		if errors.Is(observationCtx.Err(), context.DeadlineExceeded) {
			// The observation deadline is not a process deadline. The Agent owns
			// the process independently, so return the last known snapshot and let
			// the caller continue via process_output.
			last.Running = true
			last.ExitCode = nil
			return last, nil
		}
		return workspacesdk.ProcessOutputResponse{}, xerrors.Errorf("observe workspace process %s: %w", processID, err)
	}
}

func startWorkspaceProcessWithinObservation(
	ctx context.Context,
	conn workspacesdk.AgentConn,
	req workspacesdk.StartProcessRequest,
	budget mcpObservationBudget,
) (workspacesdk.StartProcessResponse, error) {
	if budget.remaining() <= 0 {
		return workspacesdk.StartProcessResponse{}, xerrors.Errorf("the %.0f-second MCP observation budget elapsed before process submission; retry after the workspace is ready", budget.window.Seconds())
	}

	startCtx, cancel := budget.context(ctx)
	defer cancel()

	started, err := conn.StartProcess(startCtx, req)
	if err == nil {
		return started, nil
	}
	if ctx.Err() == nil && errors.Is(startCtx.Err(), context.DeadlineExceeded) {
		return workspacesdk.StartProcessResponse{}, xerrors.Errorf("process start acknowledgement exceeded the %.0f-second MCP observation budget; the process may already have been submitted. Do not start it again until process_list confirms whether it exists: %w", budget.window.Seconds(), err)
	}
	return workspacesdk.StartProcessResponse{}, err
}

func workspaceProcessSnapshot(
	ctx context.Context,
	conn workspacesdk.AgentConn,
	processID string,
	cursor *int64,
	limit int,
) (workspacesdk.ProcessOutputResponse, error) {
	resp, err := conn.ProcessOutput(ctx, processID, &workspacesdk.ProcessOutputOptions{Cursor: cursor, Limit: limit})
	if err != nil {
		return workspacesdk.ProcessOutputResponse{}, xerrors.Errorf("get process output snapshot: %w", err)
	}
	return resp, nil
}

func waitForWorkspaceProcessOptions(
	ctx context.Context,
	conn workspacesdk.AgentConn,
	processID string,
	wait time.Duration,
	cursor *int64,
	limit int,
) (workspacesdk.ProcessOutputResponse, error) {
	if wait <= 0 {
		return workspaceProcessSnapshot(ctx, conn, processID, cursor, limit)
	}

	parentCtx := ctx
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	resp, err := conn.ProcessOutput(waitCtx, processID, &workspacesdk.ProcessOutputOptions{Wait: true, Cursor: cursor, Limit: limit})
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
	snapshot, snapshotErr := workspaceProcessSnapshot(snapshotCtx, conn, processID, cursor, limit)
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
			if len(process.Argv) > 0 {
				return argvAdvisories(process.Argv)
			}
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
		GapBytes:   resp.GapBytes,
		HasMore:    resp.HasMore,
		Advisories: advisories,
	}
}
