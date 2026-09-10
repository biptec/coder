package codersdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"
)

type WorkspaceCommandActivitySource string

const (
	WorkspaceCommandActivitySourceAgentProc       WorkspaceCommandActivitySource = "agentproc"
	WorkspaceCommandActivitySourceSSH             WorkspaceCommandActivitySource = "ssh"
	WorkspaceCommandActivitySourceMCP             WorkspaceCommandActivitySource = "mcp"
	WorkspaceCommandActivitySourceReconnectingPTY WorkspaceCommandActivitySource = "reconnecting_pty"
	WorkspaceCommandActivitySourceVSCode          WorkspaceCommandActivitySource = "vscode"
	WorkspaceCommandActivitySourceJetBrains       WorkspaceCommandActivitySource = "jetbrains"
	WorkspaceCommandActivitySourceChat            WorkspaceCommandActivitySource = "chat"
)

type WorkspaceCommandActivityKind string

const (
	WorkspaceCommandActivityKindCommand WorkspaceCommandActivityKind = "command"
	WorkspaceCommandActivityKindTool    WorkspaceCommandActivityKind = "tool"
)

type WorkspaceCommandActivityStatus string

const (
	WorkspaceCommandActivityStatusRunning     WorkspaceCommandActivityStatus = "running"
	WorkspaceCommandActivityStatusSucceeded   WorkspaceCommandActivityStatus = "succeeded"
	WorkspaceCommandActivityStatusFailed      WorkspaceCommandActivityStatus = "failed"
	WorkspaceCommandActivityStatusInterrupted WorkspaceCommandActivityStatus = "interrupted"
)

type WorkspaceCommandActivitySort string

const (
	WorkspaceCommandActivitySortID       WorkspaceCommandActivitySort = "id"
	WorkspaceCommandActivitySortStatus   WorkspaceCommandActivitySort = "status"
	WorkspaceCommandActivitySortStarted  WorkspaceCommandActivitySort = "started"
	WorkspaceCommandActivitySortDuration WorkspaceCommandActivitySort = "duration"
	WorkspaceCommandActivitySortTool     WorkspaceCommandActivitySort = "tool"
	WorkspaceCommandActivitySortSource   WorkspaceCommandActivitySort = "source"
	WorkspaceCommandActivitySortCommand  WorkspaceCommandActivitySort = "command"
	WorkspaceCommandActivitySortExit     WorkspaceCommandActivitySort = "exit"
)

type WorkspaceCommandActivitySortDirection string

const (
	WorkspaceCommandActivitySortAscending  WorkspaceCommandActivitySortDirection = "asc"
	WorkspaceCommandActivitySortDescending WorkspaceCommandActivitySortDirection = "desc"
)

type WorkspaceCommandActivity struct {
	ID         uuid.UUID                      `json:"id" format:"uuid"`
	AgentID    uuid.UUID                      `json:"agent_id" format:"uuid"`
	SessionID  uuid.UUID                      `json:"session_id" format:"uuid"`
	Source     WorkspaceCommandActivitySource `json:"source"`
	Kind       WorkspaceCommandActivityKind   `json:"kind,omitempty"`
	Tool       string                         `json:"tool,omitempty"`
	Command    string                         `json:"command,omitempty"`
	Argv       []string                       `json:"argv,omitempty"`
	WorkDir    string                         `json:"work_dir,omitempty"`
	Status     WorkspaceCommandActivityStatus `json:"status"`
	StartedAt  time.Time                      `json:"started_at" format:"date-time"`
	FinishedAt *time.Time                     `json:"finished_at,omitempty" format:"date-time"`
	ExitCode   *int                           `json:"exit_code,omitempty"`
}

// WorkspaceMCPRequestActivity is the minimal MCP request lifecycle exposed to
// Activity History when Idle rows are enabled. Tool input stays server-side;
// the browser only needs timing/status to derive busy and idle intervals.
type WorkspaceMCPRequestActivity struct {
	ID         uuid.UUID                      `json:"id" format:"uuid"`
	Status     WorkspaceCommandActivityStatus `json:"status"`
	StartedAt  time.Time                      `json:"started_at" format:"date-time"`
	FinishedAt *time.Time                     `json:"finished_at,omitempty" format:"date-time"`
}

type WorkspaceCommandActivityFilter struct {
	ID            string                           `json:"id,omitempty"`
	Statuses      []WorkspaceCommandActivityStatus `json:"statuses,omitempty"`
	Tools         []string                         `json:"tools,omitempty"`
	Sources       []WorkspaceCommandActivitySource `json:"sources,omitempty"`
	Search        string                           `json:"search,omitempty"`
	StartedAfter  *time.Time                       `json:"started_after,omitempty" format:"date-time"`
	StartedBefore *time.Time                       `json:"started_before,omitempty" format:"date-time"`
	DurationMinMS *int64                           `json:"duration_min_ms,omitempty"`
	DurationMaxMS *int64                           `json:"duration_max_ms,omitempty"`
	ExitCode      *int                             `json:"exit_code,omitempty"`
}

type WorkspaceCommandActivityRequest struct {
	WorkspaceCommandActivityFilter
	SortBy        WorkspaceCommandActivitySort          `json:"sort_by,omitempty"`
	SortDirection WorkspaceCommandActivitySortDirection `json:"sort_direction,omitempty"`
	Page          int                                   `json:"page,omitempty"`
	PageSize      int                                   `json:"page_size,omitempty"`
	IncludeIdle   bool                                  `json:"include_idle,omitempty"`
}

type WorkspaceCommandActivityResponse struct {
	Activity       []WorkspaceCommandActivity    `json:"activity"`
	TotalCount     int64                         `json:"total_count"`
	DeletableCount int64                         `json:"deletable_count"`
	AvailableTools []string                      `json:"available_tools"`
	Page           int                           `json:"page"`
	PageSize       int                           `json:"page_size"`
	TotalPages     int                           `json:"total_pages"`
	HistoryLimit   int64                         `json:"history_limit"`
	MCPRequests    []WorkspaceMCPRequestActivity `json:"mcp_requests,omitempty"`
}

type WorkspaceActivityWatchEventType string

const (
	WorkspaceActivityWatchEventCommandUpsert    WorkspaceActivityWatchEventType = "command_upsert"
	WorkspaceActivityWatchEventCommandResync    WorkspaceActivityWatchEventType = "command_resync"
	WorkspaceActivityWatchEventMCPRequestUpsert WorkspaceActivityWatchEventType = "mcp_request_upsert"
)

// WorkspaceActivityWatchEvent is emitted by the one-way workspace activity
// WebSocket. Command events contain one changed row. Resync is reserved for bulk
// mutations where emitting every changed row would be more expensive.
type WorkspaceActivityWatchEvent struct {
	Type       WorkspaceActivityWatchEventType `json:"type"`
	Command    *WorkspaceCommandActivity       `json:"command,omitempty"`
	MCPRequest *WorkspaceMCPRequestActivity    `json:"mcp_request,omitempty"`
}

type WorkspaceCommandActivityDeleteMode string

const (
	WorkspaceCommandActivityDeleteFiltered WorkspaceCommandActivityDeleteMode = "filtered"
	WorkspaceCommandActivityDeleteSelected WorkspaceCommandActivityDeleteMode = "selected"
)

type DeleteWorkspaceCommandActivityRequest struct {
	Mode   WorkspaceCommandActivityDeleteMode `json:"mode"`
	IDs    []uuid.UUID                        `json:"ids,omitempty" format:"uuid"`
	Filter WorkspaceCommandActivityFilter     `json:"filter,omitempty"`
}

type DeleteWorkspaceCommandActivityResponse struct {
	Deleted int64 `json:"deleted"`
}

func (r WorkspaceCommandActivityRequest) queryValues() url.Values {
	q := url.Values{}
	if r.ID != "" {
		q.Set("id", r.ID)
	}
	for _, status := range r.Statuses {
		q.Add("status", string(status))
	}
	for _, tool := range r.Tools {
		q.Add("tool", tool)
	}
	for _, source := range r.Sources {
		q.Add("source", string(source))
	}
	if r.Search != "" {
		q.Set("search", r.Search)
	}
	if r.StartedAfter != nil {
		q.Set("started_after", r.StartedAfter.Format(time.RFC3339Nano))
	}
	if r.StartedBefore != nil {
		q.Set("started_before", r.StartedBefore.Format(time.RFC3339Nano))
	}
	if r.DurationMinMS != nil {
		q.Set("duration_min_ms", strconv.FormatInt(*r.DurationMinMS, 10))
	}
	if r.DurationMaxMS != nil {
		q.Set("duration_max_ms", strconv.FormatInt(*r.DurationMaxMS, 10))
	}
	if r.ExitCode != nil {
		q.Set("exit_code", strconv.Itoa(*r.ExitCode))
	}
	if r.SortBy != "" {
		q.Set("sort_by", string(r.SortBy))
	}
	if r.SortDirection != "" {
		q.Set("sort_direction", string(r.SortDirection))
	}
	if r.Page > 0 {
		q.Set("page", strconv.Itoa(r.Page))
	}
	if r.PageSize > 0 {
		q.Set("page_size", strconv.Itoa(r.PageSize))
	}
	if r.IncludeIdle {
		q.Set("include_idle", "true")
	}
	return q
}

func (c *Client) WorkspaceCommandActivity(ctx context.Context, workspaceID uuid.UUID) (WorkspaceCommandActivityResponse, error) {
	return c.WorkspaceCommandActivityWithFilter(ctx, workspaceID, WorkspaceCommandActivityRequest{})
}

func (c *Client) WorkspaceCommandActivityWithFilter(ctx context.Context, workspaceID uuid.UUID, req WorkspaceCommandActivityRequest) (WorkspaceCommandActivityResponse, error) {
	path := "/api/v2/workspaces/" + workspaceID.String() + "/command-activity"
	if encoded := req.queryValues().Encode(); encoded != "" {
		path += "?" + encoded
	}
	res, err := c.Request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return WorkspaceCommandActivityResponse{}, xerrors.Errorf("execute request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return WorkspaceCommandActivityResponse{}, ReadBodyAsError(res)
	}
	var response WorkspaceCommandActivityResponse
	return response, json.NewDecoder(res.Body).Decode(&response)
}

func (c *Client) DeleteWorkspaceCommandActivity(ctx context.Context, workspaceID uuid.UUID, req DeleteWorkspaceCommandActivityRequest) (DeleteWorkspaceCommandActivityResponse, error) {
	res, err := c.Request(ctx, http.MethodDelete, "/api/v2/workspaces/"+workspaceID.String()+"/command-activity", req)
	if err != nil {
		return DeleteWorkspaceCommandActivityResponse{}, xerrors.Errorf("execute request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return DeleteWorkspaceCommandActivityResponse{}, ReadBodyAsError(res)
	}
	var response DeleteWorkspaceCommandActivityResponse
	return response, json.NewDecoder(res.Body).Decode(&response)
}
