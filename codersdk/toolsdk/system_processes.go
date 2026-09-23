package toolsdk

import (
	"context"
	"encoding/base64"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

type WorkspaceListSystemProcessesArgs struct {
	Workspace string `json:"workspace"`
	Cursor    string `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Filter    string `json:"filter,omitempty"`
}

type WorkspaceListSystemProcessesResult struct {
	Processes  []workspacesdk.SystemProcessInfo `json:"processes"`
	NextCursor string                           `json:"next_cursor,omitempty"`
	HasMore    bool                             `json:"has_more"`
}

var WorkspaceListSystemProcesses = Tool[WorkspaceListSystemProcessesArgs, WorkspaceListSystemProcessesResult]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceListSystemProcesses,
		Description: `List operating system processes visible inside a Coder workspace.

This is a point-in-time OS process table, analogous to ps. It is intentionally
separate from list_sessions: list_sessions returns only durable processes
tracked by Coder for recovery and interaction, while this tool can include any
process in the workspace. signal_process accepts tracked process IDs, not OS
PIDs returned by this tool.

Results are sorted by real process start time, newest first, with PID as a
stable tie-breaker. limit is required: use 0 to return all matching processes
in the current OS snapshot, or a positive value to bound the result. filter
performs a case-insensitive substring match against username and command.
cursor is an opaque continuation token returned by a previous limited call and
continues toward older processes.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{
					"type":        "string",
					"description": "The workspace name in format [owner/]workspace[.agent].",
				},
				"cursor": map[string]any{
					"type":        "string",
					"description": "Opaque continuation cursor returned by a previous list_processes call.",
					"minLength":   1,
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Required process limit. Use 0 to return all matching processes, or a positive value to bound the result.",
					"minimum":     0,
				},
				"filter": map[string]any{
					"type":        "string",
					"description": "Optional case-insensitive substring matched against process username and command.",
				},
			},
			Required: []string{"workspace", "limit"},
		},
	},
	MCPAnnotations: mcpReadOnlyAnnotations,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceListSystemProcessesArgs) (WorkspaceListSystemProcessesResult, error) {
		if strings.TrimSpace(args.Workspace) == "" {
			return WorkspaceListSystemProcessesResult{}, xerrors.New("workspace name cannot be empty")
		}
		if args.Limit < 0 {
			return WorkspaceListSystemProcessesResult{}, xerrors.New("limit cannot be negative")
		}

		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return WorkspaceListSystemProcessesResult{}, err
		}
		defer conn.Close()

		operationCtx, cancel := budget.context(ctx)
		defer cancel()
		response, err := conn.ListSystemProcesses(operationCtx)
		if err != nil {
			return WorkspaceListSystemProcessesResult{}, xerrors.Errorf("list workspace system processes: %w", workspaceAgentToolError(err))
		}
		return paginateSystemProcesses(response.Processes, args.Filter, args.Cursor, args.Limit)
	},
}

func encodeSystemProcessCursor(startedAt int64, pid int32) string {
	value := "v2:" + strconv.FormatInt(startedAt, 10) + ":" + strconv.FormatInt(int64(pid), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeSystemProcessCursor(cursor string) (int64, int32, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, 0, xerrors.New("invalid process cursor")
	}
	parts := strings.Split(string(decoded), ":")
	if len(parts) != 3 || parts[0] != "v2" {
		return 0, 0, xerrors.New("invalid process cursor")
	}
	startedAt, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || startedAt < 0 {
		return 0, 0, xerrors.New("invalid process cursor")
	}
	pid, err := strconv.ParseInt(parts[2], 10, 32)
	if err != nil || pid < 0 {
		return 0, 0, xerrors.New("invalid process cursor")
	}
	return startedAt, int32(pid), nil
}

func paginateSystemProcesses(processes []workspacesdk.SystemProcessInfo, filter, cursor string, limit int) (WorkspaceListSystemProcessesResult, error) {
	if limit < 0 {
		return WorkspaceListSystemProcessesResult{}, xerrors.New("limit cannot be negative")
	}

	var cursorStartedAt int64
	var cursorPID int32
	if cursor != "" {
		var err error
		cursorStartedAt, cursorPID, err = decodeSystemProcessCursor(cursor)
		if err != nil {
			return WorkspaceListSystemProcessesResult{}, err
		}
	}

	sorted := append([]workspacesdk.SystemProcessInfo(nil), processes...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].StartedAtUnix != sorted[j].StartedAtUnix {
			return sorted[i].StartedAtUnix > sorted[j].StartedAtUnix
		}
		return sorted[i].PID < sorted[j].PID
	})

	filter = strings.ToLower(strings.TrimSpace(filter))
	matches := func(process workspacesdk.SystemProcessInfo) bool {
		if filter == "" {
			return true
		}
		return strings.Contains(strings.ToLower(process.Username), filter) ||
			strings.Contains(strings.ToLower(process.Command), filter)
	}

	eligible := make([]workspacesdk.SystemProcessInfo, 0, len(sorted))
	for _, process := range sorted {
		if !matches(process) {
			continue
		}
		if cursor != "" {
			afterCursor := process.StartedAtUnix < cursorStartedAt ||
				(process.StartedAtUnix == cursorStartedAt && process.PID > cursorPID)
			if !afterCursor {
				continue
			}
		}
		eligible = append(eligible, process)
	}

	hasMore := limit > 0 && len(eligible) > limit
	if hasMore {
		eligible = eligible[:limit]
	}
	result := WorkspaceListSystemProcessesResult{
		Processes: eligible,
		HasMore:   hasMore,
	}
	if hasMore && len(eligible) > 0 {
		last := eligible[len(eligible)-1]
		result.NextCursor = encodeSystemProcessCursor(last.StartedAtUnix, last.PID)
	}
	return result, nil
}
