package toolsdk

import (
	"context"

	"github.com/coder/aisdk-go"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/workspacesdk"
)

type WorkspaceSearchStartArgs struct {
	Workspace     string `json:"workspace"`
	Root          string `json:"root"`
	Query         string `json:"query"`
	Mode          string `json:"mode"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	IncludeHidden bool   `json:"include_hidden,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
}

type WorkspaceSearchStartResult struct {
	SearchID string `json:"search_id"`
}

var WorkspaceSearchStart = Tool[WorkspaceSearchStartArgs, WorkspaceSearchStartResult]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceSearchStart,
		Description: `Start a bounded asynchronous workspace search. Mode "files" matches relative paths; mode "content" matches file lines. Regex uses Go RE2 semantics.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace":      map[string]any{"type": "string", "description": workspaceAgentDescription},
				"root":           map[string]any{"type": "string", "description": "Absolute search root."},
				"query":          map[string]any{"type": "string", "description": "Literal text or RE2 expression when regex=true."},
				"mode":           map[string]any{"type": "string", "enum": []string{"files", "content"}, "description": "Search relative paths or file content."},
				"regex":          map[string]any{"type": "boolean", "description": "Interpret query as a Go RE2 regular expression."},
				"case_sensitive": map[string]any{"type": "boolean", "description": "Use case-sensitive matching. Defaults to false."},
				"include_hidden": map[string]any{"type": "boolean", "description": "Include dot-prefixed files and directories."},
				"max_results":    map[string]any{"type": "integer", "description": "Maximum retained results. Defaults to 500, maximum 5000.", "minimum": 1, "maximum": 5000},
			},
			Required: []string{"workspace", "root", "query", "mode"},
		},
	},
	MCPAnnotations:     mcpReadOnlyAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceSearchStartArgs) (WorkspaceSearchStartResult, error) {
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return WorkspaceSearchStartResult{}, err
		}
		defer conn.Close()
		resp, err := conn.StartSearch(ctx, workspacesdk.SearchStartRequest{
			Root:          args.Root,
			Query:         args.Query,
			Mode:          args.Mode,
			Regex:         args.Regex,
			CaseSensitive: args.CaseSensitive,
			IncludeHidden: args.IncludeHidden,
			MaxResults:    args.MaxResults,
		})
		if err != nil {
			return WorkspaceSearchStartResult{}, xerrors.Errorf("start workspace search: %w", err)
		}
		return WorkspaceSearchStartResult{SearchID: resp.ID}, nil
	},
}

type WorkspaceSearchResultsArgs struct {
	Workspace string `json:"workspace"`
	SearchID  string `json:"search_id"`
	Cursor    int    `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

var WorkspaceSearchResults = Tool[WorkspaceSearchResultsArgs, workspacesdk.SearchResultsResponse]{
	Tool: aisdk.Tool{
		Name:        ToolNameWorkspaceSearchResults,
		Description: `Read a paginated snapshot of a workspace search session. While status is running, next_cursor may equal the current result count; call again later to observe new results.`,
		Schema: aisdk.Schema{
			Properties: map[string]any{
				"workspace": map[string]any{"type": "string", "description": workspaceAgentDescription},
				"search_id": map[string]any{"type": "string", "description": "Search session ID returned by search_start."},
				"cursor":    map[string]any{"type": "integer", "description": "Zero-based result cursor. Defaults to 0.", "minimum": 0},
				"limit":     map[string]any{"type": "integer", "description": "Maximum results returned. Defaults to 100, maximum 1000.", "minimum": 1, "maximum": 1000},
			},
			Required: []string{"workspace", "search_id"},
		},
	},
	MCPAnnotations:     mcpReadOnlyAnnotations,
	UserClientOptional: true,
	Handler: func(ctx context.Context, deps Deps, args WorkspaceSearchResultsArgs) (workspacesdk.SearchResultsResponse, error) {
		if args.Cursor < 0 {
			return workspacesdk.SearchResultsResponse{}, xerrors.New("cursor cannot be negative")
		}
		limit := args.Limit
		if limit == 0 {
			limit = 100
		}
		if limit < 1 || limit > 1000 {
			return workspacesdk.SearchResultsResponse{}, xerrors.New("limit must be between 1 and 1000")
		}
		conn, err := openAgentConn(ctx, deps, args.Workspace)
		if err != nil {
			return workspacesdk.SearchResultsResponse{}, err
		}
		defer conn.Close()
		return conn.SearchResults(ctx, args.SearchID, args.Cursor, limit)
	},
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
				"search_id": map[string]any{"type": "string", "description": "Search session ID returned by search_start."},
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
