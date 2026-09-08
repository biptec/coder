package codersdk

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"
)

type WorkspaceCommandActivitySource string

const (
	WorkspaceCommandActivitySourceAgentProc WorkspaceCommandActivitySource = "agentproc"
	WorkspaceCommandActivitySourceSSH       WorkspaceCommandActivitySource = "ssh"
)

type WorkspaceCommandActivityStatus string

const (
	WorkspaceCommandActivityStatusRunning     WorkspaceCommandActivityStatus = "running"
	WorkspaceCommandActivityStatusSucceeded   WorkspaceCommandActivityStatus = "succeeded"
	WorkspaceCommandActivityStatusFailed      WorkspaceCommandActivityStatus = "failed"
	WorkspaceCommandActivityStatusInterrupted WorkspaceCommandActivityStatus = "interrupted"
)

type WorkspaceCommandActivity struct {
	ID         uuid.UUID                      `json:"id" format:"uuid"`
	AgentID    uuid.UUID                      `json:"agent_id" format:"uuid"`
	SessionID  uuid.UUID                      `json:"session_id" format:"uuid"`
	Source     WorkspaceCommandActivitySource `json:"source"`
	Command    string                         `json:"command,omitempty"`
	Argv       []string                       `json:"argv,omitempty"`
	WorkDir    string                         `json:"work_dir,omitempty"`
	Status     WorkspaceCommandActivityStatus `json:"status"`
	StartedAt  time.Time                      `json:"started_at" format:"date-time"`
	FinishedAt *time.Time                     `json:"finished_at,omitempty" format:"date-time"`
	ExitCode   *int                           `json:"exit_code,omitempty"`
}

type WorkspaceCommandActivityResponse struct {
	Activity     []WorkspaceCommandActivity `json:"activity"`
	HistoryLimit int64                      `json:"history_limit"`
}

func (c *Client) WorkspaceCommandActivity(ctx context.Context, workspaceID uuid.UUID) (WorkspaceCommandActivityResponse, error) {
	res, err := c.Request(ctx, http.MethodGet, "/api/v2/workspaces/"+workspaceID.String()+"/command-activity", nil)
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
