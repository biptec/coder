package toolsdk

import (
	"context"
	"time"

	"golang.org/x/xerrors"

	"github.com/coder/aisdk-go"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

const searchPollInterval = 50 * time.Millisecond

type WorkspaceSearchStartArgs struct {
	Workspace     string `json:"workspace"`
	Root          string `json:"root"`
	Query         string `json:"query"`
	Mode          string `json:"mode"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	IncludeHidden bool   `json:"include_hidden,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
	WaitTimeoutMs *int   `json:"wait_timeout_ms,omitempty"`
}

var WorkspaceSearchStart = Tool[WorkspaceSearchStartArgs, workspacesdk.SearchResultsResponse]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceSearchStart,
		Description: `Start an asynchronous workspace search and return the initial snapshot.

Mode "files" matches relative paths; mode "content" matches file lines. Regex uses
Go RE2 semantics. Omit wait_timeout_ms or use 0 to return immediately after the
Agent creates the search session; a positive value observes for initial results.
The search keeps running independently after this MCP call. max_results is a
required explicit retention limit: use 0 for no logical result-count cap, or a
positive value to retain at most that many results. Continue with get_search_results when more results are needed.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace":      map[string]any{"type": "string", "description": workspaceAgentDescription},
				"root":           map[string]any{"type": "string", "description": "Absolute search root."},
				"query":          map[string]any{"type": "string", "description": "Literal text or RE2 expression when regex=true."},
				"mode":           map[string]any{"type": "string", "enum": []string{"files", "content"}, "description": "Search relative paths or file content."},
				"regex":          map[string]any{"type": "boolean", "description": "Interpret query as a Go RE2 regular expression."},
				"case_sensitive": map[string]any{"type": "boolean", "description": "Use case-sensitive matching. Defaults to false."},
				"include_hidden": map[string]any{"type": "boolean", "description": "Include dot-prefixed files and directories."},
				"max_results":    map[string]any{"type": "integer", "description": "Required retained-result limit. Use 0 for no logical result-count cap, or a positive value to retain at most that many results.", "minimum": 0},
				"wait_timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Optional initial result observation interval in milliseconds. Omit or use 0 to return immediately. This never limits the search lifetime.",
					"minimum":     0,
				},
			},
			Required: []string{"workspace", "root", "query", "mode", "max_results"},
		},
	},
	MCPAnnotations:     mcpReadOnlyNonIdempotentAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceSearchStartArgs) (workspacesdk.SearchResultsResponse, error) {
		if args.Workspace == "" {
			return workspacesdk.SearchResultsResponse{}, xerrors.New("workspace cannot be empty")
		}
		if args.Root == "" {
			return workspacesdk.SearchResultsResponse{}, xerrors.New("root cannot be empty")
		}
		if args.Query == "" {
			return workspacesdk.SearchResultsResponse{}, xerrors.New("query cannot be empty")
		}
		if args.Mode != "files" && args.Mode != "content" {
			return workspacesdk.SearchResultsResponse{}, xerrors.New(`mode must be "files" or "content"`)
		}
		if args.MaxResults < 0 {
			return workspacesdk.SearchResultsResponse{}, xerrors.New("max_results cannot be negative")
		}

		wait, err := workspaceSearchWaitDuration(args.WaitTimeoutMs, deps.MCPToolTimeoutMax())
		if err != nil {
			return workspacesdk.SearchResultsResponse{}, err
		}

		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return workspacesdk.SearchResultsResponse{}, err
		}
		defer conn.Close()

		operationCtx, cancel := budget.context(ctx)
		defer cancel()
		started, err := conn.StartSearch(operationCtx, workspacesdk.SearchStartRequest{
			Root:          args.Root,
			Query:         args.Query,
			Mode:          args.Mode,
			Regex:         args.Regex,
			CaseSensitive: args.CaseSensitive,
			IncludeHidden: args.IncludeHidden,
			MaxResults:    args.MaxResults,
		})
		if err != nil {
			return workspacesdk.SearchResultsResponse{}, xerrors.Errorf("start workspace search: %w", err)
		}

		fallback := workspacesdk.SearchResultsResponse{Search: workspacesdk.SearchSessionInfo{
			ID:     started.ID,
			Root:   args.Root,
			Query:  args.Query,
			Mode:   args.Mode,
			Status: "running",
		}}
		wait = workspaceProcessWaitWithinBudget(wait, budget)
		observed, observeErr := observeWorkspaceSearch(operationCtx, conn, started.ID, 0, 0, wait)
		if observeErr != nil {
			// The search session already exists. Preserve the handle instead of
			// returning an error that could encourage an unnecessary duplicate.
			return fallback, nil
		}
		return observed, nil
	},
}

type WorkspaceSearchResultsArgs struct {
	Workspace     string `json:"workspace"`
	SearchID      string `json:"search_id"`
	Cursor        int    `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	WaitTimeoutMs *int   `json:"wait_timeout_ms,omitempty"`
}

var WorkspaceSearchResults = Tool[WorkspaceSearchResultsArgs, workspacesdk.SearchResultsResponse]{
	Tool: aisdk.Tool{
		Name: ToolNameWorkspaceSearchResults,
		Description: `Read a paginated snapshot of a workspace search session.

Without wait_timeout_ms (or with 0), return the current snapshot immediately.
With an explicit wait, continue observing until new results are available, the
search completes, or the observation interval ends. Waiting never controls the
search lifetime.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"search_id": map[string]any{"type": "string", "description": "Search session ID returned by start_search."},
				"cursor":    map[string]any{"type": "integer", "description": "Zero-based result cursor. Defaults to 0.", "minimum": 0},
				"limit":     map[string]any{"type": "integer", "description": "Required result limit. Use 0 to return all results currently available from the cursor, or a positive value to bound the page.", "minimum": 0},
				"wait_timeout_ms": map[string]any{
					"type":        "integer",
					"description": "Optional result observation interval in milliseconds. Omit or use 0 for an immediate snapshot.",
					"minimum":     0,
				},
			},
			Required: []string{"workspace", "search_id", "limit"},
		},
	},
	MCPAnnotations:     mcpReadOnlyAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceSearchResultsArgs) (workspacesdk.SearchResultsResponse, error) {
		if args.Workspace == "" {
			return workspacesdk.SearchResultsResponse{}, xerrors.New("workspace cannot be empty")
		}
		if args.SearchID == "" {
			return workspacesdk.SearchResultsResponse{}, xerrors.New("search_id cannot be empty")
		}
		if args.Cursor < 0 {
			return workspacesdk.SearchResultsResponse{}, xerrors.New("cursor cannot be negative")
		}
		limit := args.Limit
		if limit < 0 {
			return workspacesdk.SearchResultsResponse{}, xerrors.New("limit cannot be negative")
		}
		wait, err := workspaceSearchWaitDuration(args.WaitTimeoutMs, deps.MCPToolTimeoutMax())
		if err != nil {
			return workspacesdk.SearchResultsResponse{}, err
		}

		budget := newMCPObservationBudget(deps)
		conn, err := openAgentConnWithBudget(ctx, deps, args.Workspace, budget)
		if err != nil {
			return workspacesdk.SearchResultsResponse{}, err
		}
		defer conn.Close()

		operationCtx, cancel := budget.context(ctx)
		defer cancel()
		wait = workspaceProcessWaitWithinBudget(wait, budget)
		return observeWorkspaceSearch(operationCtx, conn, args.SearchID, args.Cursor, limit, wait)
	},
}

func workspaceSearchWaitDuration(waitTimeoutMs *int, maxWait time.Duration) (time.Duration, error) {
	return workspaceProcessWaitDuration(waitTimeoutMs, maxWait)
}

func observeWorkspaceSearch(
	ctx context.Context,
	conn workspacesdk.AgentConn,
	searchID string,
	cursor int,
	limit int,
	wait time.Duration,
) (workspacesdk.SearchResultsResponse, error) {
	deadline := time.Now().Add(wait)
	var last workspacesdk.SearchResultsResponse
	for {
		resp, err := conn.SearchResults(ctx, searchID, cursor, limit)
		if err != nil {
			return workspacesdk.SearchResultsResponse{}, xerrors.Errorf("read workspace search results: %w", err)
		}
		last = resp
		if wait <= 0 || resp.Search.Status != "running" || len(resp.Results) > 0 {
			return resp, nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return last, nil
		}
		delay := searchPollInterval
		if remaining < delay {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			if last.Search.ID != "" {
				return last, nil
			}
			return workspacesdk.SearchResultsResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
}

type WorkspaceSearchListArgs struct {
	Workspace string `json:"workspace"`
}

var WorkspaceSearchList = Tool[WorkspaceSearchListArgs, workspacesdk.ListSearchesResponse]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceSearchList,
		Description: `List active and recently completed workspace search sessions visible to this chat context.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
			},
			Required: []string{"workspace"},
		},
	},
	MCPAnnotations:     mcpReadOnlyAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceSearchListArgs) (workspacesdk.ListSearchesResponse, error) {
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return workspacesdk.ListSearchesResponse{}, err
		}
		defer conn.Close()
		return conn.ListSearches(ctx)
	},
}

type WorkspaceSearchStopArgs struct {
	Workspace string `json:"workspace"`
	SearchID  string `json:"search_id"`
}

var WorkspaceSearchStop = Tool[WorkspaceSearchStopArgs, codersdk.Response]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceSearchStop,
		Description: `Cancel a running workspace search session. This only affects ephemeral search state and does not modify workspace files.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"search_id": map[string]any{"type": "string", "description": "Search session ID returned by start_search."},
			},
			Required: []string{"workspace", "search_id"},
		},
	},
	MCPAnnotations:     mcpReadOnlyAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceSearchStopArgs) (codersdk.Response, error) {
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return codersdk.Response{}, err
		}
		defer conn.Close()
		if err := conn.StopSearch(ctx, args.SearchID); err != nil {
			return codersdk.Response{}, xerrors.Errorf("stop workspace search: %w", err)
		}
		return codersdk.Response{Message: "Search stop requested."}, nil
	},
}
