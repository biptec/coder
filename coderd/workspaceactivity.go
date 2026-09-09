package coderd

import (
	"net/http"
	"sort"
	"time"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/codersdk"
)

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
	historyLimit := int32(api.DeploymentValues.WorkspaceCommandActivityHistoryLimit.Value()) // #nosec G115 -- deployment config is validated to int32 range.

	rows, err := api.Database.GetWorkspaceCommandActivityByWorkspaceID(ctx, database.GetWorkspaceCommandActivityByWorkspaceIDParams{
		WorkspaceID:  workspace.ID,
		HistoryLimit: historyLimit,
	})
	if dbauthz.IsNotAuthorizedError(err) {
		httpapi.Forbidden(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}

	activity := make([]codersdk.WorkspaceCommandActivity, 0, len(rows))
	for _, row := range rows {
		item := codersdk.WorkspaceCommandActivity{
			ID:        row.ID,
			AgentID:   row.AgentID,
			SessionID: row.SessionID,
			Source:    codersdk.WorkspaceCommandActivitySource(row.Source),
			Tool:      row.Tool,
			Command:   row.Command,
			Argv:      append([]string(nil), row.Argv...),
			WorkDir:   row.WorkDir,
			Status:    codersdk.WorkspaceCommandActivityStatus(row.Status),
			StartedAt: row.StartedAt,
		}
		if row.FinishedAt.Valid {
			finishedAt := row.FinishedAt.Time
			item.FinishedAt = &finishedAt
		}
		if row.ExitCode.Valid {
			exitCode := int(row.ExitCode.Int32)
			item.ExitCode = &exitCode
		}
		activity = append(activity, item)
	}

	httpapi.Write(ctx, rw, http.StatusOK, codersdk.WorkspaceCommandActivityResponse{
		Activity:     activity,
		HistoryLimit: int64(historyLimit),
	})
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

	rows, err := api.Database.GetWorkspaceConnectionActivityByWorkspaceID(ctx, workspace.ID)
	if dbauthz.IsNotAuthorizedError(err) {
		httpapi.Forbidden(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}

	byType := map[codersdk.ConnectionType]*codersdk.WorkspaceConnectionActivityType{}
	for _, connectionType := range []codersdk.ConnectionType{
		codersdk.ConnectionTypeSSH,
		codersdk.ConnectionTypeReconnectingPTY,
		codersdk.ConnectionTypeVSCode,
		codersdk.ConnectionTypeJetBrains,
	} {
		byType[connectionType] = &codersdk.WorkspaceConnectionActivityType{Type: connectionType}
	}

	var response codersdk.WorkspaceConnectionActivityResponse
	for _, row := range rows {
		connectionType := codersdk.ConnectionType(row.Type)
		summary, ok := byType[connectionType]
		if !ok {
			// This endpoint describes long-lived agent connections. Web app and
			// port-forwarding connection logs do not have reliable close events.
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
	response.Active = response.ActiveConnections > 0

	response.Types = make([]codersdk.WorkspaceConnectionActivityType, 0, len(byType))
	for _, summary := range byType {
		response.Types = append(response.Types, *summary)
	}
	sort.Slice(response.Types, func(i, j int) bool {
		return connectionActivityTypeOrder(response.Types[i].Type) < connectionActivityTypeOrder(response.Types[j].Type)
	})

	httpapi.Write(ctx, rw, http.StatusOK, response)
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
	default:
		return 100
	}
}
