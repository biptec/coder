package toolsdk

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const processSnapshotTimeout = 5 * time.Second

func applyInvocationScopeHeader(ctx context.Context, conn workspacesdk.AgentConn) {
	scope := InvocationScopeFromContext(ctx)
	if scope == "" {
		return
	}
	headers := make(http.Header, 1)
	headers.Set(workspacesdk.CoderInvocationScopeHeader, scope)
	conn.SetExtraHeaders(headers)
}

type mcpObservationBudget struct {
	deadline time.Time
	window   time.Duration
	max      time.Duration
}

func newMCPObservationBudget(deps Deps) mcpObservationBudget {
	maxTimeout := deps.MCPToolTimeoutMax()
	return mcpObservationBudget{
		deadline: time.Now().Add(maxTimeout),
		window:   maxTimeout,
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

func processLaunchFingerprint(req workspacesdk.StartProcessRequest) (string, error) {
	payload := struct {
		Command      string            `json:"command,omitempty"`
		Argv         []string          `json:"argv,omitempty"`
		WorkDir      string            `json:"workdir,omitempty"`
		Env          map[string]string `json:"env,omitempty"`
		Interactive  bool              `json:"interactive,omitempty"`
		Stdin        string            `json:"stdin,omitempty"`
		Host         string            `json:"host,omitempty"`
		IdentityFile string            `json:"identity_file,omitempty"`
		Port         int               `json:"port,omitempty"`
	}{
		Command: req.Command, Argv: req.Argv, WorkDir: req.WorkDir, Env: req.Env,
		Interactive: req.Interactive, Stdin: req.Stdin, Host: req.Host,
		IdentityFile: req.IdentityFile, Port: req.Port,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("%x", digest[:]), nil
}

type WorkspaceProcessStartV2Args struct {
	Workspace      string               `json:"workspace"`
	Argv           []string             `json:"argv"`
	WorkDir        string               `json:"workdir,omitempty"`
	Env            map[string]string    `json:"env,omitempty"`
	Interactive    bool                 `json:"interactive,omitempty"`
	Stdin          string               `json:"stdin,omitempty"`
	SSH            *WorkspaceSSHOptions `json:"ssh,omitempty"`
	WaitTimeoutMs  *int                 `json:"wait_timeout_ms,omitempty"`
	AllowDuplicate bool                 `json:"allow_duplicate,omitempty"`
}

var WorkspaceProcessStartV2 = Tool[WorkspaceProcessStartV2Args, WorkspaceProcessResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceProcessStartV2,
		Description: `Start a durable process by executable argv without shell parsing.

This is the canonical structured execution tool. argv[0] is the executable and
each later element is passed as exactly one argument. Use execute_shell_command
when shell syntax such as pipes, redirects, &&, loops, or expansion is
intentional.

The process is durable and survives the MCP request. After the workspace Agent
acknowledges the start, this call observes initial output only when a positive
wait_timeout_ms is requested. Omit it or use 0 for an immediate snapshot.
Observation time never limits process lifetime.

The response always includes process_id plus current state and initial output.
If the process is still running, continue with read_process_output. If start
acknowledgement is lost after submission, use list_sessions before retrying
because the process may already exist.

Set interactive=true only when later interact_with_process calls are required.
Set ssh to execute on a remote host through the workspace OpenSSH client.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": "The workspace name in format [owner/]workspace[.agent]."},
				"argv": map[string]any{
					"type": "array", "description": "Executable and arguments. No shell parsing is performed.",
					"minItems": 1, "items": map[string]any{"type": "string"},
				},
				"workdir": map[string]any{"type": "string", "description": "Optional working directory."},
				"env": map[string]any{
					"type": "object", "description": "Optional environment variable overrides.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"interactive": map[string]any{"type": "boolean", "description": "Keep stdin open for later interact_with_process calls. Defaults to false."},
				"stdin": map[string]any{
					"type": "string", "description": "Optional initial stdin. Non-interactive mode sends EOF after this content; interactive mode keeps stdin open.",
				},
				"ssh":             workspaceSSHSchema(),
				"allow_duplicate": map[string]any{"type": "boolean", "description": "Start a second identical concurrently running process. Defaults to false; normally an identical running process is reused instead."},
				"wait_timeout_ms": map[string]any{
					"type": "integer", "description": "Optional initial output observation interval in milliseconds. Omit or use 0 for an immediate snapshot. This never limits the process lifetime.",
					"minimum": 0,
				},
			},
			Required: []string{"workspace", "argv"},
		},
	},
	MCPAnnotations: mcpExecutionAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceProcessStartV2Args) (WorkspaceProcessResult, error) {
		if args.Workspace == "" {
			return WorkspaceProcessResult{}, xerrors.New("workspace name cannot be empty")
		}
		if len(args.Argv) == 0 || args.Argv[0] == "" {
			return WorkspaceProcessResult{}, xerrors.New("argv must contain a non-empty executable at argv[0]")
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
			Argv: args.Argv, WorkDir: args.WorkDir, Env: args.Env,
			Tool: InvocationToolFromContext(ctx), Interactive: args.Interactive, Stdin: args.Stdin,
		}
		if err := applyWorkspaceSSHOptions(&request, args.SSH); err != nil {
			return WorkspaceProcessResult{}, err
		}
		fingerprint, err := processLaunchFingerprint(request)
		if err != nil {
			return WorkspaceProcessResult{}, xerrors.Errorf("fingerprint workspace process launch: %w", err)
		}
		request.Fingerprint = fingerprint
		request.AllowDuplicate = args.AllowDuplicate
		advisories := argvAdvisories(args.Argv)
		started, err := startWorkspaceProcessWithinObservation(ctx, conn, request, budget)
		if err != nil {
			return WorkspaceProcessResult{}, xerrors.Errorf("start workspace process: %w", err)
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

// WorkspaceProcessResult is the state returned for a tracked workspace process.
type WorkspaceProcessResult struct {
	Output          string                          `json:"output"`
	DuplicateReused bool                            `json:"duplicate_reused,omitempty"`
	ExitCode        *int                            `json:"exit_code,omitempty"`
	ProcessID       string                          `json:"process_id"`
	Running         bool                            `json:"running"`
	Truncated       *workspacesdk.ProcessTruncation `json:"truncated,omitempty"`
	NextCursor      *int64                          `json:"next_cursor,omitempty"`
	GapBytes        int64                           `json:"gap_bytes,omitempty"`
	HasMore         bool                            `json:"has_more,omitempty"`
	Advisories      []ToolAdvisory                  `json:"advisories,omitempty"`
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
					"description": "Optional output-wait interval in milliseconds. Omit it (or use 0) for an immediate snapshot. It cannot exceed the deployment-wide MCP tool timeout, and the actual wait may be shorter when workspace readiness consumes part of the request budget. This never limits the process lifetime.",
					"minimum":     0,
				},
				"cursor": map[string]any{
					"type":        "integer",
					"description": "Optional absolute byte cursor for incremental output. Start with 0 and continue with next_cursor.",
					"minimum":     0,
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Required output-byte limit. Use 0 to return all currently retained output from the cursor, or a positive value to bound the response.",
					"minimum":     0,
				},
			},
			Required: []string{"workspace", "process_id", "limit"},
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
		if args.Limit < 0 {
			return WorkspaceProcessResult{}, xerrors.New("limit cannot be negative")
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
	Cursor    string `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type WorkspaceProcessInfo struct {
	workspacesdk.ProcessInfo
	Advisories []ToolAdvisory `json:"advisories,omitempty"`
}

type WorkspaceProcessListResult struct {
	Processes  []WorkspaceProcessInfo `json:"processes"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

func encodeTrackedProcessCursor(startedAt int64, id string) string {
	value := fmt.Sprintf("v1:%d:%s", startedAt, id)
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeTrackedProcessCursor(cursor string) (int64, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", xerrors.New("invalid process cursor")
	}
	parts := strings.SplitN(string(decoded), ":", 3)
	if len(parts) != 3 || parts[0] != "v1" || parts[2] == "" {
		return 0, "", xerrors.New("invalid process cursor")
	}
	startedAt, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || startedAt < 0 {
		return 0, "", xerrors.New("invalid process cursor")
	}
	return startedAt, parts[2], nil
}

var WorkspaceProcessList = Tool[WorkspaceProcessListArgs, WorkspaceProcessListResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceProcessList,
		Description: `List durable processes tracked by a Coder workspace agent.

Use this after a timeout, 502, reconnect, or any uncertain command result before running the command again. It lets you recover the original process_id and inspect whether the command is still running or already exited. Results are ordered by start time, newest first. An optional opaque cursor continues toward older sessions. Processes whose command invokes sudo include a structured persistence advisory alongside their metadata.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "The workspace name in format [owner/]workspace[.agent]. If owner is omitted, the authenticated user is used.",
				},
				"cursor": map[string]any{
					"type":        "string",
					"description": "Opaque newest-first continuation cursor returned by a previous list_sessions call.",
					"minLength":   1,
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Required session limit. Use 0 to return all tracked sessions, or a positive value to bound the result.",
					"minimum":     0,
				},
			},
			Required: []string{"workspace", "limit"},
		},
	},
	MCPAnnotations: mcpReadOnlyAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceProcessListArgs) (WorkspaceProcessListResult, error) {
		if args.Workspace == "" {
			return WorkspaceProcessListResult{}, xerrors.New("workspace name cannot be empty")
		}
		if args.Limit < 0 {
			return WorkspaceProcessListResult{}, xerrors.New("limit cannot be negative")
		}

		var cursorStartedAt int64
		var cursorID string
		if args.Cursor != "" {
			var err error
			cursorStartedAt, cursorID, err = decodeTrackedProcessCursor(args.Cursor)
			if err != nil {
				return WorkspaceProcessListResult{}, err
			}
		}

		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return WorkspaceProcessListResult{}, err
		}
		defer conn.Close()

		operationCtx, cancel := budget.context(ctx)
		defer cancel()
		resp, err := conn.ListProcesses(operationCtx)
		if err != nil {
			return WorkspaceProcessListResult{}, xerrors.Errorf("list workspace processes: %w", err)
		}

		sort.Slice(resp.Processes, func(i, j int) bool {
			if resp.Processes[i].StartedAt != resp.Processes[j].StartedAt {
				return resp.Processes[i].StartedAt > resp.Processes[j].StartedAt
			}
			return resp.Processes[i].ID < resp.Processes[j].ID
		})

		eligible := make([]workspacesdk.ProcessInfo, 0, len(resp.Processes))
		for _, process := range resp.Processes {
			if args.Cursor != "" {
				afterCursor := process.StartedAt < cursorStartedAt ||
					(process.StartedAt == cursorStartedAt && process.ID > cursorID)
				if !afterCursor {
					continue
				}
			}
			eligible = append(eligible, process)
		}

		hasMore := args.Limit > 0 && len(eligible) > args.Limit
		if hasMore {
			eligible = eligible[:args.Limit]
		}
		processes := make([]WorkspaceProcessInfo, 0, len(eligible))
		for _, process := range eligible {
			advisories := commandAdvisories(process.Command)
			if len(process.Argv) > 0 {
				advisories = argvAdvisories(process.Argv)
			}
			processes = append(processes, WorkspaceProcessInfo{
				ProcessInfo: process,
				Advisories:  advisories,
			})
		}

		result := WorkspaceProcessListResult{Processes: processes}
		if hasMore && len(processes) > 0 {
			last := processes[len(processes)-1].ProcessInfo
			result.NextCursor = encodeTrackedProcessCursor(last.StartedAt, last.ID)
		}
		return result, nil
	},
}

type WorkspaceProcessInputArgs struct {
	Workspace     string `json:"workspace"`
	ProcessID     string `json:"process_id"`
	Data          string `json:"data,omitempty"`
	Close         bool   `json:"close,omitempty"`
	WaitTimeoutMs *int   `json:"wait_timeout_ms,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type WorkspaceProcessInputResult struct {
	Success     bool                    `json:"success"`
	Message     string                  `json:"message"`
	Process     *WorkspaceProcessResult `json:"process,omitempty"`
	OutputError string                  `json:"output_error,omitempty"`
}

var WorkspaceProcessInput = Tool[WorkspaceProcessInputArgs, WorkspaceProcessInputResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceProcessInput,
		Description: `Write to stdin of a durable process started with interactive=true and return an output snapshot.

Use close=true to send EOF after optional data. Before writing, the server checkpoints the current output cursor so the response contains only output observed after this interaction. Omit wait_timeout_ms or use 0 for an immediate post-write snapshot; use a positive value to observe for new output. limit is required: use 0 for all newly retained output, or a positive value to bound the response.

Input delivery and output observation are deliberately reported separately. If
stdin was accepted but the follow-up output read fails, success remains true and
output_error describes the observation failure. This prevents callers from
retrying a non-idempotent input write and accidentally sending the same data
twice.

This tool only targets durable processes tracked by the workspace Agent and
preserves chat/process isolation.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "The workspace name in format [owner/]workspace[.agent].",
				},
				"process_id": map[string]any{
					"type":        "string",
					"description": "Tracked process ID returned by coder_workspace_process_start or coder_workspace_process_list.",
				},
				"data": map[string]any{
					"type":        "string",
					"description": "Data to write verbatim to process stdin.",
				},
				"close": map[string]any{
					"type":        "boolean",
					"description": "Close stdin after writing data, sending EOF.",
				},
				"wait_timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Optional post-input output observation interval in milliseconds. Omit or use 0 for an immediate snapshot. It cannot exceed the deployment-wide MCP tool timeout.",
					"minimum":     0,
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Required incremental output-byte limit. Use 0 for all newly retained output, or a positive value to bound the response.",
					"minimum":     0,
				},
			},
			Required: []string{"workspace", "process_id", "limit"},
		},
	},
	MCPAnnotations: mcpExecutionAnnotations,
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
		if args.Limit < 0 {
			return WorkspaceProcessInputResult{}, xerrors.New("limit cannot be negative")
		}
		wait, err := workspaceInitialProcessWaitDuration(args.WaitTimeoutMs, deps.MCPToolTimeoutMax())
		if err != nil {
			return WorkspaceProcessInputResult{}, err
		}

		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return WorkspaceProcessInputResult{}, err
		}
		defer conn.Close()

		return interactWithWorkspaceProcess(ctx, conn, args, wait, budget)
	},
}

// workspaceProcessTailCursor asks the Agent to clamp an intentionally future
// cursor to the current end of the stream. This avoids replaying old output
// before a non-idempotent stdin interaction.
func workspaceProcessTailCursor(ctx context.Context, conn workspacesdk.AgentConn, processID string) (int64, error) {
	cursor := int64(9223372036854775807)
	resp, err := conn.ProcessOutput(ctx, processID, &workspacesdk.ProcessOutputOptions{
		Cursor: &cursor,
		Limit:  1,
	})
	if err != nil {
		return 0, err
	}
	return resp.NextCursor, nil
}

func interactWithWorkspaceProcess(
	ctx context.Context,
	conn workspacesdk.AgentConn,
	args WorkspaceProcessInputArgs,
	wait time.Duration,
	budget mcpObservationBudget,
) (WorkspaceProcessInputResult, error) {
	operationCtx, cancel := budget.context(ctx)
	defer cancel()

	cursor, err := workspaceProcessTailCursor(operationCtx, conn, args.ProcessID)
	if err != nil {
		return WorkspaceProcessInputResult{}, xerrors.Errorf("checkpoint workspace process output: %w", err)
	}

	if err := conn.ProcessInput(operationCtx, args.ProcessID, workspacesdk.ProcessInputRequest{Data: args.Data, Close: args.Close}); err != nil {
		return WorkspaceProcessInputResult{}, xerrors.Errorf("send workspace process input: %w", err)
	}

	result := WorkspaceProcessInputResult{
		Success: true,
		Message: "process input sent",
	}

	wait = workspaceProcessWaitWithinBudget(wait, budget)
	resp, err := waitForWorkspaceProcessOptions(operationCtx, conn, args.ProcessID, wait, &cursor, args.Limit)
	if err != nil {
		// The input was already accepted. Do not return a top-level error that
		// could encourage a caller to retry the non-idempotent write.
		result.OutputError = err.Error()
		return result, nil
	}

	advisories := workspaceProcessAdvisories(operationCtx, conn, args.ProcessID)
	processResult := workspaceProcessResult(args.ProcessID, resp, advisories)
	processResult.Output = resp.Output
	next := resp.NextCursor
	processResult.NextCursor = &next
	result.Process = &processResult
	return result, nil
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

Use signal "interrupt" to request Ctrl-C/SIGINT semantics, "terminate" for graceful SIGTERM shutdown, or "kill" to force stop. Always identify the intended process with coder_workspace_process_list first when there is any ambiguity.`,
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
					"enum":        []string{"interrupt", "terminate", "kill"},
				},
			},
			Required: []string{"workspace", "process_id", "signal"},
		},
	},
	MCPAnnotations: mcpExecutionAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceProcessSignalArgs) (WorkspaceProcessSignalResult, error) {
		if args.Workspace == "" {
			return WorkspaceProcessSignalResult{}, xerrors.New("workspace name cannot be empty")
		}
		if args.ProcessID == "" {
			return WorkspaceProcessSignalResult{}, xerrors.New("process_id cannot be empty")
		}
		if args.Signal != "interrupt" && args.Signal != "terminate" && args.Signal != "kill" {
			return WorkspaceProcessSignalResult{}, xerrors.New(`signal must be "interrupt", "terminate", or "kill"`)
		}

		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return WorkspaceProcessSignalResult{}, err
		}
		defer conn.Close()

		operationCtx, cancel := budget.context(ctx)
		defer cancel()
		return signalWorkspaceProcess(operationCtx, conn, args.ProcessID, args.Signal)
	},
}

func signalWorkspaceProcess(
	ctx context.Context,
	conn workspacesdk.AgentConn,
	processID string,
	signal string,
) (WorkspaceProcessSignalResult, error) {
	if err := conn.SignalProcess(ctx, processID, signal); err != nil {
		var sdkErr *codersdk.Error
		if !errors.As(err, &sdkErr) || sdkErr.StatusCode() != http.StatusConflict {
			return WorkspaceProcessSignalResult{}, xerrors.Errorf("signal workspace process: %w", err)
		}
		// HTTP 409 means the tracked session exists but is no longer signalable.
		// A signal can race with natural process completion, so confirm its final
		// state before translating that conflict into an idempotent result.
		if processes, listErr := conn.ListProcesses(ctx); listErr == nil {
			for _, process := range processes.Processes {
				if process.ID == processID && !process.Running {
					return WorkspaceProcessSignalResult{
						Success: true,
						Message: fmt.Sprintf("Process %s is already completed.", processID),
					}, nil
				}
			}
		}
		return WorkspaceProcessSignalResult{}, xerrors.Errorf("signal workspace process: %w", err)
	}
	return WorkspaceProcessSignalResult{
		Success: true,
		Message: fmt.Sprintf("signal %q sent to process %s", signal, processID),
	}, nil
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

func workspaceInitialProcessWaitDuration(waitTimeoutMs *int, maxWait time.Duration) (time.Duration, error) {
	return workspaceProcessWaitDuration(waitTimeoutMs, maxWait)
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
		return 0, xerrors.Errorf("wait_timeout_ms cannot exceed deployment MCP tool timeout of %dms", maxWait.Milliseconds())
	}
	return wait, nil
}

// observeInitialWorkspaceProcess waits across intermediate output events until
// the process exits or the requested initial observation window ends, then reads
// one cursor-based snapshot from byte zero so callers receive both initial output
// and a continuation cursor. The observation window never controls process lifetime.
func observeInitialWorkspaceProcess(
	ctx context.Context,
	conn workspacesdk.AgentConn,
	processID string,
	wait time.Duration,
	budget mcpObservationBudget,
) (workspacesdk.ProcessOutputResponse, error) {
	wait = workspaceProcessWaitWithinBudget(wait, budget)
	var observeErr error
	if wait > 0 {
		observationBudget := budget
		waitDeadline := time.Now().Add(wait)
		if waitDeadline.Before(observationBudget.deadline) {
			observationBudget.deadline = waitDeadline
		}
		_, observeErr = observeWorkspaceProcess(ctx, conn, processID, observationBudget)
		if ctx.Err() != nil {
			return workspacesdk.ProcessOutputResponse{}, observeErr
		}
	}

	snapshotCtx, cancel := context.WithTimeout(ctx, processSnapshotTimeout)
	defer cancel()
	cursor := int64(0)
	resp, snapshotErr := conn.ProcessOutput(snapshotCtx, processID, &workspacesdk.ProcessOutputOptions{
		Cursor: &cursor,
	})
	if snapshotErr == nil {
		return resp, nil
	}
	if observeErr != nil {
		return workspacesdk.ProcessOutputResponse{}, xerrors.Errorf("observe initial process output: %v; final snapshot failed: %w", observeErr, snapshotErr)
	}
	return workspacesdk.ProcessOutputResponse{}, xerrors.Errorf("get initial process output snapshot: %w", snapshotErr)
}

// observeWorkspaceProcess keeps a request open across intermediate output
// events until the observation deadline or process exit. It is also used by
// legacy exec, whose process lifetime remains independent of the request.
func observeWorkspaceProcess(
	ctx context.Context,
	conn workspacesdk.AgentConn,
	processID string,
	budget mcpObservationBudget,
) (workspacesdk.ProcessOutputResponse, error) {
	// If readiness and StartProcess already consumed the observation budget, we
	// still have a durable process ID. Return control immediately rather than
	// opening another wait interval.
	last := workspacesdk.ProcessOutputResponse{Running: true}
	if budget.remaining() <= 0 {
		return last, nil
	}

	observationCtx, cancel := budget.context(ctx)
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
		recoveryTool := "process_list"
		switch InvocationToolFromContext(ctx) {
		case "start_process", "execute_shell_command":
			recoveryTool = "list_sessions"
		}
		return workspacesdk.StartProcessResponse{}, xerrors.Errorf("process start acknowledgement exceeded the %.0f-second MCP observation budget; the process may already have been submitted. Do not start it again until %s confirms whether it exists: %w", budget.window.Seconds(), recoveryTool, err)
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

	var exitCode *int
	if !resp.Running {
		code := 0
		if resp.ExitCode != nil {
			code = *resp.ExitCode
		}
		exitCode = &code
	}
	return WorkspaceProcessResult{
		Output:     resp.Output,
		ExitCode:   exitCode,
		ProcessID:  processID,
		Running:    resp.Running,
		Truncated:  resp.Truncated,
		GapBytes:   resp.GapBytes,
		HasMore:    resp.HasMore,
		Advisories: advisories,
	}
}
