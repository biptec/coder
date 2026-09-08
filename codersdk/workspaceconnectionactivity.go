package codersdk

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"
)

// WorkspaceConnectionActivityType summarizes connection activity of one type
// across every agent in a workspace.
type WorkspaceConnectionActivityType struct {
	Type               ConnectionType `json:"type"`
	ActiveConnections  int32          `json:"active_connections"`
	LastConnectedAt    *time.Time     `json:"last_connected_at,omitempty" format:"date-time"`
	LastDisconnectedAt *time.Time     `json:"last_disconnected_at,omitempty" format:"date-time"`
	LastActivityAt     *time.Time     `json:"last_activity_at,omitempty" format:"date-time"`
}

type WorkspaceConnectionActivityResponse struct {
	Active            bool                              `json:"active"`
	ActiveConnections int32                             `json:"active_connections"`
	LastActivityAt    *time.Time                        `json:"last_activity_at,omitempty" format:"date-time"`
	Types             []WorkspaceConnectionActivityType `json:"types"`
}

func (c *Client) WorkspaceConnectionActivity(ctx context.Context, workspaceID uuid.UUID) (WorkspaceConnectionActivityResponse, error) {
	res, err := c.Request(ctx, http.MethodGet, "/api/v2/workspaces/"+workspaceID.String()+"/connection-activity", nil)
	if err != nil {
		return WorkspaceConnectionActivityResponse{}, xerrors.Errorf("execute request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return WorkspaceConnectionActivityResponse{}, ReadBodyAsError(res)
	}
	var response WorkspaceConnectionActivityResponse
	return response, json.NewDecoder(res.Body).Decode(&response)
}
