package coderd

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/mcp"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/codersdk"
)

const (
	defaultWorkspaceCommandActivityPageSize = 50
	maxWorkspaceCommandActivityPageSize     = 500
	staleWorkspaceMCPRequestHeartbeatGrace  = time.Minute
)

type workspaceCommandActivityDBFilter struct {
	idSearch      string
	statuses      []string
	tools         []string
	sources       []string
	search        string
	startedAfter  time.Time
	startedBefore time.Time
	durationMinMS int64
	durationMaxMS int64
	exitCode      sql.NullInt32
}

// @Summary Get workspace command activity
// @ID get-workspace-command-activity
// @Security CoderSessionToken
// @Produce json
// @Tags Workspaces
// @Param workspace path string true "Workspace ID" format(uuid)
// @Success 200 {object} codersdk.WorkspaceCommandActivityResponse
// @Router /api/v2/workspaces/{workspace}/command-activity [get]
func (api *API) workspaceCommandActivity(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspace := httpmw.WorkspaceParam(r)

	req, err := parseWorkspaceCommandActivityQuery(r.URL.Query())
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: err.Error()})
		return
	}
	filter, err := workspaceCommandActivityFilter(req.WorkspaceCommandActivityFilter)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: err.Error()})
		return
	}

	page := req.Page
	if page <= 0 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize <= 0 {
		pageSize = defaultWorkspaceCommandActivityPageSize
	}
	if pageSize > maxWorkspaceCommandActivityPageSize {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: fmt.Sprintf("page_size cannot exceed %d", maxWorkspaceCommandActivityPageSize)})
		return
	}
	offset64 := int64(page-1) * int64(pageSize)
	if offset64 > math.MaxInt32 {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "page is too large"})
		return
	}

	sortBy := req.SortBy
	if sortBy == "" {
		sortBy = codersdk.WorkspaceCommandActivitySortStarted
	}
	sortDirection := req.SortDirection
	if sortDirection == "" {
		sortDirection = codersdk.WorkspaceCommandActivitySortDescending
	}
	if !validWorkspaceCommandActivitySort(sortBy) {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: fmt.Sprintf("unsupported sort_by %q", sortBy)})
		return
	}
	if sortDirection != codersdk.WorkspaceCommandActivitySortAscending && sortDirection != codersdk.WorkspaceCommandActivitySortDescending {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: fmt.Sprintf("unsupported sort_direction %q", sortDirection)})
		return
	}

	if req.IdleOnly {
		if sortBy != codersdk.WorkspaceCommandActivitySortStarted {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "idle_only supports sort_by=started only"})
			return
		}
		if err := api.interruptStaleWorkspaceMCPRequestActivity(ctx, workspace.ID); err != nil {
			if dbauthz.IsNotAuthorizedError(err) {
				httpapi.Forbidden(rw)
				return
			}
			httpapi.InternalServerError(rw, err)
			return
		}
		totalCount, err := api.Database.CountWorkspaceIdleActivity(ctx, database.CountWorkspaceIdleActivityParams{
			WorkspaceID:   workspace.ID,
			StartedAfter:  filter.startedAfter,
			StartedBefore: filter.startedBefore,
			DurationMinMs: filter.durationMinMS,
			DurationMaxMs: filter.durationMaxMS,
		})
		if dbauthz.IsNotAuthorizedError(err) {
			httpapi.Forbidden(rw)
			return
		}
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}
		rows, err := api.Database.ListWorkspaceIdleActivity(ctx, database.ListWorkspaceIdleActivityParams{
			SortDirection: string(sortDirection),
			PageOffset:    int32(offset64), // #nosec G115 -- bounded above by MaxInt32.
			PageLimit:     int32(pageSize), // #nosec G115 -- bounded above by 500.
			WorkspaceID:   workspace.ID,
			StartedAfter:  filter.startedAfter,
			StartedBefore: filter.startedBefore,
			DurationMinMs: filter.durationMinMS,
			DurationMaxMs: filter.durationMaxMS,
		})
		if dbauthz.IsNotAuthorizedError(err) {
			httpapi.Forbidden(rw)
			return
		}
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}
		idleActivity := make([]codersdk.WorkspaceIdleActivity, 0, len(rows))
		for _, row := range rows {
			var finishedAt *time.Time
			if !row.IsCurrent {
				value := row.FinishedAt
				finishedAt = &value
			}
			idleActivity = append(idleActivity, codersdk.WorkspaceIdleActivity{
				StartedAt:  row.StartedAt,
				FinishedAt: finishedAt,
			})
		}
		availableTools, err := api.workspaceActivityToolNames(ctx, r, workspace.ID)
		if dbauthz.IsNotAuthorizedError(err) {
			httpapi.Forbidden(rw)
			return
		}
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}
		totalPages := 0
		if totalCount > 0 {
			totalPages = int((totalCount + int64(pageSize) - 1) / int64(pageSize))
		}
		httpapi.Write(ctx, rw, http.StatusOK, codersdk.WorkspaceCommandActivityResponse{
			Activity:       []codersdk.WorkspaceCommandActivity{},
			TotalCount:     totalCount,
			DeletableCount: 0,
			AvailableTools: availableTools,
			Page:           page,
			PageSize:       pageSize,
			TotalPages:     totalPages,
			HistoryLimit:   api.DeploymentValues.WorkspaceCommandActivityHistoryLimit.Value(),
			IdleActivity:   idleActivity,
		})
		return
	}

	totalCount, err := api.Database.CountWorkspaceCommandActivity(ctx, filter.countParams(workspace.ID))
	if dbauthz.IsNotAuthorizedError(err) {
		httpapi.Forbidden(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}

	rows, err := api.Database.ListWorkspaceCommandActivity(ctx, database.ListWorkspaceCommandActivityParams{
		WorkspaceID:   workspace.ID,
		IDSearch:      filter.idSearch,
		Statuses:      filter.statuses,
		Tools:         filter.tools,
		Sources:       filter.sources,
		Search:        filter.search,
		StartedAfter:  filter.startedAfter,
		StartedBefore: filter.startedBefore,
		DurationMinMs: filter.durationMinMS,
		DurationMaxMs: filter.durationMaxMS,
		ExitCode:      filter.exitCode,
		SortBy:        string(sortBy),
		SortDirection: string(sortDirection),
		PageOffset:    int32(offset64), // #nosec G115 -- bounded above by MaxInt32.
		PageLimit:     int32(pageSize), // #nosec G115 -- bounded above by 500.
	})
	activity := make([]codersdk.WorkspaceCommandActivity, 0, len(rows))
	if err == nil {
		for _, row := range rows {
			activity = append(activity, workspaceCommandActivityFromDatabase(row))
		}
	}
	deletableCount := totalCount
	if dbauthz.IsNotAuthorizedError(err) {
		httpapi.Forbidden(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}

	availableTools, err := api.workspaceActivityToolNames(ctx, r, workspace.ID)
	if dbauthz.IsNotAuthorizedError(err) {
		httpapi.Forbidden(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}

	var mcpRequests []codersdk.WorkspaceMCPRequestActivity
	if req.IncludeIdle {
		mcpRequests, err = api.workspaceMCPRequestActivityForResponse(ctx, workspace.ID, sortBy, activity)
		if dbauthz.IsNotAuthorizedError(err) {
			httpapi.Forbidden(rw)
			return
		}
		if err != nil {
			httpapi.InternalServerError(rw, err)
			return
		}
	}

	totalPages := 0
	if totalCount > 0 {
		totalPages = int((totalCount + int64(pageSize) - 1) / int64(pageSize))
	}

	httpapi.Write(ctx, rw, http.StatusOK, codersdk.WorkspaceCommandActivityResponse{
		Activity:       activity,
		TotalCount:     totalCount,
		DeletableCount: deletableCount,
		AvailableTools: availableTools,
		Page:           page,
		PageSize:       pageSize,
		TotalPages:     totalPages,
		HistoryLimit:   api.DeploymentValues.WorkspaceCommandActivityHistoryLimit.Value(),
		MCPRequests:    mcpRequests,
	})
}

// @Summary Delete workspace command activity
// @ID delete-workspace-command-activity
// @Security CoderSessionToken
// @Accept json
// @Produce json
// @Tags Workspaces
// @Param workspace path string true "Workspace ID" format(uuid)
// @Param request body codersdk.DeleteWorkspaceCommandActivityRequest true "Delete command activity"
// @Success 200 {object} codersdk.DeleteWorkspaceCommandActivityResponse
// @Router /api/v2/workspaces/{workspace}/command-activity [delete]
func (api *API) deleteWorkspaceCommandActivity(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspace := httpmw.WorkspaceParam(r)
	var req codersdk.DeleteWorkspaceCommandActivityRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}

	var deleted int64
	var err error
	switch req.Mode {
	case codersdk.WorkspaceCommandActivityDeleteSelected:
		if len(req.IDs) == 0 {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "selected delete requires at least one id"})
			return
		}
		deleted, err = api.Database.DeleteWorkspaceCommandActivityByIDs(ctx, database.DeleteWorkspaceCommandActivityByIDsParams{
			WorkspaceID: workspace.ID,
			IDs:         req.IDs,
		})
	case codersdk.WorkspaceCommandActivityDeleteFiltered:
		filter, filterErr := workspaceCommandActivityFilter(req.Filter)
		if filterErr != nil {
			httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: filterErr.Error()})
			return
		}
		deleted, err = api.Database.DeleteWorkspaceCommandActivityByFilter(ctx, database.DeleteWorkspaceCommandActivityByFilterParams{
			WorkspaceID:   workspace.ID,
			IDSearch:      filter.idSearch,
			Statuses:      filter.statuses,
			Tools:         filter.tools,
			Sources:       filter.sources,
			Search:        filter.search,
			StartedAfter:  filter.startedAfter,
			StartedBefore: filter.startedBefore,
			DurationMinMs: filter.durationMinMS,
			DurationMaxMs: filter.durationMaxMS,
			ExitCode:      filter.exitCode,
		})
	default:
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "mode must be either filtered or selected"})
		return
	}
	if dbauthz.IsNotAuthorizedError(err) {
		httpapi.Forbidden(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	if deleted > 0 {
		if err := coderdpubsub.PublishWorkspaceActivityEvent(api.Pubsub, workspace.ID, coderdpubsub.WorkspaceActivityEvent{
			Type: coderdpubsub.WorkspaceActivityEventCommandResync,
		}); err != nil {
			api.Logger.Warn(ctx, "publish command activity resync", slog.Error(err), slog.F("workspace_id", workspace.ID))
		}
	}
	httpapi.Write(ctx, rw, http.StatusOK, codersdk.DeleteWorkspaceCommandActivityResponse{Deleted: deleted})
}

func parseWorkspaceCommandActivityQuery(values url.Values) (codersdk.WorkspaceCommandActivityRequest, error) {
	search := strings.TrimSpace(values.Get("search"))
	if search == "" {
		// Keep q as a compatibility alias for early clients that used it before
		// the public request field was standardized on search.
		search = strings.TrimSpace(values.Get("q"))
	}
	req := codersdk.WorkspaceCommandActivityRequest{
		WorkspaceCommandActivityFilter: codersdk.WorkspaceCommandActivityFilter{
			ID:     strings.TrimSpace(values.Get("id")),
			Search: search,
		},
		SortBy:        codersdk.WorkspaceCommandActivitySort(values.Get("sort_by")),
		SortDirection: codersdk.WorkspaceCommandActivitySortDirection(values.Get("sort_direction")),
	}
	for _, value := range queryList(values, "status") {
		req.Statuses = append(req.Statuses, codersdk.WorkspaceCommandActivityStatus(value))
	}
	for _, value := range queryList(values, "tool") {
		if value != "" {
			req.Tools = append(req.Tools, value)
		}
	}
	for _, value := range queryList(values, "source") {
		req.Sources = append(req.Sources, codersdk.WorkspaceCommandActivitySource(value))
	}

	var err error
	if req.StartedAfter, err = queryTime(values, "started_after"); err != nil {
		return req, err
	}
	if req.StartedBefore, err = queryTime(values, "started_before"); err != nil {
		return req, err
	}
	if req.DurationMinMS, err = queryOptionalInt64(values, "duration_min_ms"); err != nil {
		return req, err
	}
	if req.DurationMaxMS, err = queryOptionalInt64(values, "duration_max_ms"); err != nil {
		return req, err
	}
	if req.ExitCode, err = queryOptionalInt(values, "exit_code"); err != nil {
		return req, err
	}
	if req.Page, err = queryPositiveInt(values, "page", 1); err != nil {
		return req, err
	}
	if req.PageSize, err = queryPositiveInt(values, "page_size", defaultWorkspaceCommandActivityPageSize); err != nil {
		return req, err
	}
	if raw := strings.TrimSpace(values.Get("include_idle")); raw != "" {
		req.IncludeIdle, err = strconv.ParseBool(raw)
		if err != nil {
			return req, fmt.Errorf("include_idle must be a boolean: %w", err)
		}
	}
	if raw := strings.TrimSpace(values.Get("idle_only")); raw != "" {
		req.IdleOnly, err = strconv.ParseBool(raw)
		if err != nil {
			return req, fmt.Errorf("idle_only must be a boolean: %w", err)
		}
		if req.IdleOnly {
			req.IncludeIdle = true
		}
	}
	return req, nil
}

func queryList(values url.Values, key string) []string {
	var result []string
	for _, raw := range values[key] {
		for _, value := range strings.Split(raw, ",") {
			if value = strings.TrimSpace(value); value != "" {
				result = append(result, value)
			}
		}
	}
	return result
}

func queryTime(values url.Values, key string) (*time.Time, error) {
	raw := strings.TrimSpace(values.Get(key))
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be an RFC3339 timestamp: %w", key, err)
	}
	return &value, nil
}

func queryOptionalInt64(values url.Values, key string) (*int64, error) {
	raw := strings.TrimSpace(values.Get(key))
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return nil, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return &value, nil
}

func queryOptionalInt(values url.Values, key string) (*int, error) {
	raw := strings.TrimSpace(values.Get(key))
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be an integer", key)
	}
	return &value, nil
}

func queryPositiveInt(values url.Values, key string, fallback int) (int, error) {
	raw := strings.TrimSpace(values.Get(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func workspaceCommandActivityFilter(filter codersdk.WorkspaceCommandActivityFilter) (workspaceCommandActivityDBFilter, error) {
	result := workspaceCommandActivityDBFilter{
		idSearch:      strings.TrimSpace(filter.ID),
		search:        strings.TrimSpace(filter.Search),
		durationMinMS: -1,
		durationMaxMS: -1,
	}
	for _, status := range filter.Statuses {
		if !validWorkspaceCommandActivityStatus(status) {
			return result, fmt.Errorf("unsupported status %q", status)
		}
		result.statuses = append(result.statuses, string(status))
	}
	for _, tool := range filter.Tools {
		if tool = strings.TrimSpace(tool); tool != "" {
			result.tools = append(result.tools, tool)
		}
	}
	for _, source := range filter.Sources {
		if !validWorkspaceCommandActivitySource(source) {
			return result, fmt.Errorf("unsupported source %q", source)
		}
		result.sources = append(result.sources, string(source))
	}
	if filter.StartedAfter != nil {
		result.startedAfter = *filter.StartedAfter
	}
	if filter.StartedBefore != nil {
		result.startedBefore = *filter.StartedBefore
	}
	if !result.startedAfter.IsZero() && !result.startedBefore.IsZero() && result.startedAfter.After(result.startedBefore) {
		return result, fmt.Errorf("started_after cannot be after started_before")
	}
	if filter.DurationMinMS != nil {
		if *filter.DurationMinMS < 0 {
			return result, fmt.Errorf("duration_min_ms must be non-negative")
		}
		result.durationMinMS = *filter.DurationMinMS
	}
	if filter.DurationMaxMS != nil {
		if *filter.DurationMaxMS < 0 {
			return result, fmt.Errorf("duration_max_ms must be non-negative")
		}
		result.durationMaxMS = *filter.DurationMaxMS
	}
	if result.durationMinMS >= 0 && result.durationMaxMS >= 0 && result.durationMinMS > result.durationMaxMS {
		return result, fmt.Errorf("duration_min_ms cannot exceed duration_max_ms")
	}
	if filter.ExitCode != nil {
		if *filter.ExitCode < math.MinInt32 || *filter.ExitCode > math.MaxInt32 {
			return result, fmt.Errorf("exit_code must fit a 32-bit integer")
		}
		result.exitCode = sql.NullInt32{Int32: int32(*filter.ExitCode), Valid: true} // #nosec G115 -- range checked above.
	}
	return result, nil
}

func (f workspaceCommandActivityDBFilter) countParams(workspaceID uuid.UUID) database.CountWorkspaceCommandActivityParams {
	return database.CountWorkspaceCommandActivityParams{
		WorkspaceID:   workspaceID,
		IDSearch:      f.idSearch,
		Statuses:      f.statuses,
		Tools:         f.tools,
		Sources:       f.sources,
		Search:        f.search,
		StartedAfter:  f.startedAfter,
		StartedBefore: f.startedBefore,
		DurationMinMs: f.durationMinMS,
		DurationMaxMs: f.durationMaxMS,
		ExitCode:      f.exitCode,
	}
}

func validWorkspaceCommandActivityStatus(status codersdk.WorkspaceCommandActivityStatus) bool {
	switch status {
	case codersdk.WorkspaceCommandActivityStatusRunning,
		codersdk.WorkspaceCommandActivityStatusSucceeded,
		codersdk.WorkspaceCommandActivityStatusFailed,
		codersdk.WorkspaceCommandActivityStatusInterrupted:
		return true
	default:
		return false
	}
}

func validWorkspaceCommandActivitySource(source codersdk.WorkspaceCommandActivitySource) bool {
	switch source {
	case codersdk.WorkspaceCommandActivitySourceAgentProc,
		codersdk.WorkspaceCommandActivitySourceSSH,
		codersdk.WorkspaceCommandActivitySourceMCP,
		codersdk.WorkspaceCommandActivitySourceReconnectingPTY,
		codersdk.WorkspaceCommandActivitySourceVSCode,
		codersdk.WorkspaceCommandActivitySourceJetBrains,
		codersdk.WorkspaceCommandActivitySourceChat:
		return true
	default:
		return false
	}
}

func validWorkspaceCommandActivitySort(sortBy codersdk.WorkspaceCommandActivitySort) bool {
	switch sortBy {
	case codersdk.WorkspaceCommandActivitySortID,
		codersdk.WorkspaceCommandActivitySortStatus,
		codersdk.WorkspaceCommandActivitySortStarted,
		codersdk.WorkspaceCommandActivitySortDuration,
		codersdk.WorkspaceCommandActivitySortTool,
		codersdk.WorkspaceCommandActivitySortSource,
		codersdk.WorkspaceCommandActivitySortCommand,
		codersdk.WorkspaceCommandActivitySortExit:
		return true
	default:
		return false
	}
}

func (api *API) workspaceActivityToolNames(ctx context.Context, r *http.Request, workspaceID uuid.UUID) ([]string, error) {
	historical, err := api.Database.ListWorkspaceCommandActivityTools(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	toolset := codersdk.MCPToolsetDeveloper
	assigned, toolsetErr := api.Database.GetUserMCPToolset(ctx, httpmw.APIKey(r).UserID)
	if toolsetErr == nil {
		if candidate := codersdk.MCPToolset(assigned); candidate.Valid() {
			toolset = candidate
		}
	} else {
		// Toolset metadata is only used to enrich the filter catalog. History
		// itself must remain readable even if this optional lookup fails.
		api.Logger.Debug(ctx, "get MCP toolset for workspace activity catalog",
			slog.Error(toolsetErr),
			slog.F("user_id", httpmw.APIKey(r).UserID),
		)
	}

	unique := make(map[string]struct{}, len(historical)+32)
	for _, name := range mcp.ActivityToolNames(toolset) {
		if name != "" {
			unique[name] = struct{}{}
		}
	}
	for _, name := range historical {
		if name != "" {
			unique[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for name := range unique {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func normalizedWorkspaceCommandActivitySource(source, tool string) codersdk.WorkspaceCommandActivitySource {
	if source == "agentproc" {
		switch tool {
		case "exec", "bash", "process_start",
			"coder_workspace_exec", "coder_workspace_bash",
			"coder_workspace_process_start", "coder_workspace_process_start_v2":
			return codersdk.WorkspaceCommandActivitySourceMCP
		}
	}
	return codersdk.WorkspaceCommandActivitySource(source)
}

func workspaceMCPRequestActivityFromDatabase(row database.WorkspaceMcpRequestActivity) codersdk.WorkspaceMCPRequestActivity {
	item := codersdk.WorkspaceMCPRequestActivity{
		ID:        row.ID,
		Status:    codersdk.WorkspaceCommandActivityStatus(row.Status),
		StartedAt: row.StartedAt,
	}
	if row.FinishedAt.Valid {
		finishedAt := row.FinishedAt.Time
		item.FinishedAt = &finishedAt
	}
	return item
}

func (api *API) interruptStaleWorkspaceMCPRequestActivity(ctx context.Context, workspaceID uuid.UUID) error {
	now := time.Now().UTC()
	interrupted, err := api.Database.InterruptStaleWorkspaceMCPRequestActivity(dbauthz.AsSystemRestricted(ctx), database.InterruptStaleWorkspaceMCPRequestActivityParams{
		WorkspaceID:      workspaceID,
		CurrentReplicaID: api.ID,
		StaleBefore:      now.Add(-staleWorkspaceMCPRequestHeartbeatGrace),
	})
	if err != nil {
		return err
	}
	if interrupted > 0 {
		if err := coderdpubsub.PublishWorkspaceActivityEvent(api.Pubsub, workspaceID, coderdpubsub.WorkspaceActivityEvent{Type: coderdpubsub.WorkspaceActivityEventCommandResync}); err != nil {
			api.Logger.Debug(ctx, "publish stale MCP request activity resync", slog.Error(err), slog.F("workspace_id", workspaceID))
		}
	}
	return nil
}

func (api *API) workspaceMCPRequestActivityForResponse(
	ctx context.Context,
	workspaceID uuid.UUID,
	sortBy codersdk.WorkspaceCommandActivitySort,
	activity []codersdk.WorkspaceCommandActivity,
) ([]codersdk.WorkspaceMCPRequestActivity, error) {
	if err := api.interruptStaleWorkspaceMCPRequestActivity(ctx, workspaceID); err != nil {
		return nil, err
	}

	current, err := api.Database.ListWorkspaceMCPRequestActivityCurrent(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	rowsByID := make(map[uuid.UUID]database.WorkspaceMcpRequestActivity, len(current)+len(activity))
	for _, row := range current {
		rowsByID[row.ID] = row
	}

	// Historical Idle is meaningful only on the chronological Started sort. For
	// other sorts we still return current request state so the live Idle row stays
	// correct without loading a potentially enormous unrelated time range.
	if sortBy == codersdk.WorkspaceCommandActivitySortStarted && len(activity) > 0 {
		var rangeStart, rangeEnd time.Time
		for _, item := range activity {
			// Running activity is pinned above historical rows and can be hours or
			// days older than the chronological page. Idle is derived only from MCP
			// request spans, so a pinned process must not expand the history range.
			if item.FinishedAt == nil {
				continue
			}
			if rangeStart.IsZero() || item.StartedAt.Before(rangeStart) {
				rangeStart = item.StartedAt
			}
			if rangeEnd.IsZero() || item.FinishedAt.After(rangeEnd) {
				rangeEnd = *item.FinishedAt
			}
		}
		if !rangeStart.IsZero() {
			rows, err := api.Database.ListWorkspaceMCPRequestActivityForRange(ctx, database.ListWorkspaceMCPRequestActivityForRangeParams{
				WorkspaceID: workspaceID,
				RangeStart:  rangeStart,
				RangeEnd:    rangeEnd,
			})
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				rowsByID[row.ID] = row
			}
		}
	}

	result := make([]codersdk.WorkspaceMCPRequestActivity, 0, len(rowsByID))
	for _, row := range rowsByID {
		result = append(result, workspaceMCPRequestActivityFromDatabase(row))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartedAt.Equal(result[j].StartedAt) {
			return result[i].ID.String() < result[j].ID.String()
		}
		return result[i].StartedAt.Before(result[j].StartedAt)
	})
	return result, nil
}

func workspaceCommandActivityFromDatabase(row database.WorkspaceCommandActivity) codersdk.WorkspaceCommandActivity {
	item := codersdk.WorkspaceCommandActivity{
		ID:          row.ID,
		AgentID:     row.AgentID,
		SessionID:   row.SessionID,
		Source:      normalizedWorkspaceCommandActivitySource(row.Source, row.Tool),
		Kind:        codersdk.WorkspaceCommandActivityKind(row.Kind),
		Tool:        row.Tool,
		Command:     row.Command,
		Argv:        append([]string(nil), row.Argv...),
		Environment: map[string]string(row.Environment),
		Output:      row.Output,
		WorkDir:     row.WorkDir,
		Status:      codersdk.WorkspaceCommandActivityStatus(row.Status),
		StartedAt:   row.StartedAt,
	}
	if row.FinishedAt.Valid {
		finishedAt := row.FinishedAt.Time
		item.FinishedAt = &finishedAt
	}
	if row.ExitCode.Valid {
		exitCode := int(row.ExitCode.Int32)
		item.ExitCode = &exitCode
	}
	return item
}

// @Summary Get workspace connection activity
// @ID get-workspace-connection-activity
// @Security CoderSessionToken
// @Produce json
// @Tags Workspaces
// @Param workspace path string true "Workspace ID" format(uuid)
// @Success 200 {object} codersdk.WorkspaceConnectionActivityResponse
// @Router /api/v2/workspaces/{workspace}/connection-activity [get]
func (api *API) workspaceConnectionActivity(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspace := httpmw.WorkspaceParam(r)

	response, err := api.workspaceConnectionActivityResponse(ctx, workspace.ID)
	if dbauthz.IsNotAuthorizedError(err) {
		httpapi.Forbidden(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, response)
}

func (api *API) workspaceConnectionActivityResponse(ctx context.Context, workspaceID uuid.UUID) (codersdk.WorkspaceConnectionActivityResponse, error) {
	rows, err := api.Database.GetWorkspaceConnectionActivityByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return codersdk.WorkspaceConnectionActivityResponse{}, err
	}

	byType := map[codersdk.ConnectionType]*codersdk.WorkspaceConnectionActivityType{}
	for _, connectionType := range []codersdk.ConnectionType{
		codersdk.ConnectionTypeSSH,
		codersdk.ConnectionTypeReconnectingPTY,
		codersdk.ConnectionTypeVSCode,
		codersdk.ConnectionTypeJetBrains,
		codersdk.ConnectionTypeMCP,
	} {
		byType[connectionType] = &codersdk.WorkspaceConnectionActivityType{Type: connectionType}
	}

	var response codersdk.WorkspaceConnectionActivityResponse
	for _, row := range rows {
		connectionType := codersdk.ConnectionType(row.Type)
		summary, ok := byType[connectionType]
		if !ok {
			continue
		}

		summary.ActiveConnections += row.ActiveConnections
		response.ActiveConnections += row.ActiveConnections
		if row.LastConnectedAt.Valid {
			setLatestTime(&summary.LastConnectedAt, row.LastConnectedAt.Time)
		}
		if row.LastDisconnectedAt.Valid {
			setLatestTime(&summary.LastDisconnectedAt, row.LastDisconnectedAt.Time)
		}
		setLatestTime(&summary.LastActivityAt, row.LastActivityAt)
		setLatestTime(&response.LastActivityAt, row.LastActivityAt)
	}
	if summary := byType[codersdk.ConnectionTypeMCP]; summary != nil {
		activeMCP := api.workspaceMCPConnections.Active(workspaceID)
		response.ActiveConnections -= summary.ActiveConnections
		summary.ActiveConnections = activeMCP
		response.ActiveConnections += activeMCP
	}
	response.Active = response.ActiveConnections > 0

	response.Types = make([]codersdk.WorkspaceConnectionActivityType, 0, len(byType))
	for _, summary := range byType {
		response.Types = append(response.Types, *summary)
	}
	sort.Slice(response.Types, func(i, j int) bool {
		return connectionActivityTypeOrder(response.Types[i].Type) < connectionActivityTypeOrder(response.Types[j].Type)
	})
	return response, nil
}

// @Summary Watch workspace activity history via WebSockets
// @ID watch-workspace-activity-via-websockets
// @Security CoderSessionToken
// @Produce json
// @Tags Workspaces
// @Param workspace path string true "Workspace ID" format(uuid)
// @Success 200 {object} codersdk.ServerSentEvent
// @Router /api/v2/workspaces/{workspace}/activity/watch [get]
func (api *API) watchWorkspaceActivityWS(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspace := httpmw.WorkspaceParam(r)

	// Authorize before upgrading the connection. The subscription channel is
	// workspace-scoped, so an unauthorized client must never be able to observe
	// even event timing or command IDs.
	_, err := api.Database.CountWorkspaceCommandActivity(ctx, database.CountWorkspaceCommandActivityParams{
		WorkspaceID:   workspace.ID,
		DurationMinMs: -1,
		DurationMaxMs: -1,
	})
	if dbauthz.IsNotAuthorizedError(err) {
		httpapi.Forbidden(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}

	sendEvent, senderClosed, err := httpapi.OneWayWebSocketEventSender(api.Logger, api.wsWatcher)(rw, r)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusInternalServerError, codersdk.Response{
			Message: "Internal error setting up workspace activity WebSocket.",
			Detail:  err.Error(),
		})
		return
	}
	defer func() { <-senderClosed }()

	sendData := func(event codersdk.WorkspaceActivityWatchEvent) {
		_ = sendEvent(codersdk.ServerSentEvent{Type: codersdk.ServerSentEventTypeData, Data: event})
	}
	// PubSub callbacks must stay non-blocking: a slow browser or a DB read must
	// never stall the shared Postgres notification listener. The handler drains
	// this bounded queue and performs all database/WebSocket work itself.
	eventC := make(chan coderdpubsub.WorkspaceActivityEvent, 64)
	resyncC := make(chan struct{}, 1)
	signalResync := func() {
		select {
		case resyncC <- struct{}{}:
		default:
		}
	}
	cancel, err := api.Pubsub.SubscribeWithErr(
		coderdpubsub.WorkspaceActivityEventChannel(workspace.ID),
		coderdpubsub.HandleWorkspaceActivityEvent(func(_ context.Context, event coderdpubsub.WorkspaceActivityEvent, eventErr error) {
			if eventErr != nil {
				signalResync()
				return
			}
			if event.Type == coderdpubsub.WorkspaceActivityEventConnectionChanged {
				return
			}
			select {
			case eventC <- event:
			default:
				// If a burst exceeds the bounded delta queue, one durable REST
				// resync is safer than blocking PubSub or growing memory without bound.
				signalResync()
			}
		}),
	)
	if err != nil {
		_ = sendEvent(codersdk.ServerSentEvent{Type: codersdk.ServerSentEventTypeError, Data: codersdk.Response{
			Message: "Failed to subscribe to workspace activity.",
			Detail:  err.Error(),
		}})
		return
	}
	defer cancel()

	// Signal that subscription is installed. The browser performs its initial
	// paginated REST fetch separately and only uses this socket for deltas.
	_ = sendEvent(codersdk.ServerSentEvent{Type: codersdk.ServerSentEventTypePing})

	handleActivityEvent := func(event coderdpubsub.WorkspaceActivityEvent) {
		switch event.Type {
		case coderdpubsub.WorkspaceActivityEventCommandChanged:
			row, err := api.Database.GetWorkspaceCommandActivityByID(ctx, database.GetWorkspaceCommandActivityByIDParams{
				WorkspaceID: workspace.ID,
				ID:          event.CommandID,
			})
			if err != nil {
				// The row may have been removed by a simultaneous clear/prune. A
				// resync is cheaper and safer than trying to distinguish every race.
				sendData(codersdk.WorkspaceActivityWatchEvent{Type: codersdk.WorkspaceActivityWatchEventCommandResync})
				return
			}
			command := workspaceCommandActivityFromDatabase(row)
			sendData(codersdk.WorkspaceActivityWatchEvent{
				Type:    codersdk.WorkspaceActivityWatchEventCommandUpsert,
				Command: &command,
			})
		case coderdpubsub.WorkspaceActivityEventCommandResync:
			sendData(codersdk.WorkspaceActivityWatchEvent{Type: codersdk.WorkspaceActivityWatchEventCommandResync})
		case coderdpubsub.WorkspaceActivityEventMCPRequestChanged:
			row, err := api.Database.GetWorkspaceMCPRequestActivityByID(ctx, database.GetWorkspaceMCPRequestActivityByIDParams{
				WorkspaceID: workspace.ID,
				ID:          event.RequestID,
			})
			if err != nil {
				sendData(codersdk.WorkspaceActivityWatchEvent{Type: codersdk.WorkspaceActivityWatchEventCommandResync})
				return
			}
			request := workspaceMCPRequestActivityFromDatabase(row)
			sendData(codersdk.WorkspaceActivityWatchEvent{
				Type:       codersdk.WorkspaceActivityWatchEventMCPRequestUpsert,
				MCPRequest: &request,
			})
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-senderClosed:
			return
		case <-resyncC:
			// PubSub can explicitly report dropped notifications, and the local
			// queue can intentionally collapse bursts. In both cases restore the
			// durable activity snapshot once.
			sendData(codersdk.WorkspaceActivityWatchEvent{Type: codersdk.WorkspaceActivityWatchEventCommandResync})
		case event := <-eventC:
			handleActivityEvent(event)
		}
	}
}

func setLatestTime(dst **time.Time, candidate time.Time) {
	if candidate.IsZero() {
		return
	}
	if *dst == nil || candidate.After(**dst) {
		value := candidate
		*dst = &value
	}
}

func connectionActivityTypeOrder(connectionType codersdk.ConnectionType) int {
	switch connectionType {
	case codersdk.ConnectionTypeSSH:
		return 0
	case codersdk.ConnectionTypeReconnectingPTY:
		return 1
	case codersdk.ConnectionTypeVSCode:
		return 2
	case codersdk.ConnectionTypeJetBrains:
		return 3
	case codersdk.ConnectionTypeMCP:
		return 4
	default:
		return 100
	}
}
