package codersdk

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"
)

type WorkspaceVolumeCopyStatus string

const (
	WorkspaceVolumeCopyStatusPending   WorkspaceVolumeCopyStatus = "pending"
	WorkspaceVolumeCopyStatusRunning   WorkspaceVolumeCopyStatus = "running"
	WorkspaceVolumeCopyStatusSucceeded WorkspaceVolumeCopyStatus = "succeeded"
	WorkspaceVolumeCopyStatusFailed    WorkspaceVolumeCopyStatus = "failed"
	WorkspaceVolumeCopyStatusCanceled  WorkspaceVolumeCopyStatus = "canceled"
)

type WorkspaceVolumeCopyVolume struct {
	Key           string   `json:"key"`
	DisplayName   string   `json:"display_name"`
	MountPath     string   `json:"mount_path"`
	Capacity      string   `json:"capacity,omitempty"`
	ExcludedPaths []string `json:"excluded_paths,omitempty"`
}

type WorkspaceVolumeCopySelection struct {
	Key       string `json:"key"`
	Overwrite bool   `json:"overwrite"`
}

type CreateWorkspaceVolumeCopyRequest struct {
	DestinationWorkspaceID uuid.UUID                      `json:"destination_workspace_id" format:"uuid"`
	AllowSourceRunning     bool                           `json:"allow_source_running"`
	Volumes                []WorkspaceVolumeCopySelection `json:"volumes"`
}

type WorkspaceVolumeCopyOperation struct {
	ID                     uuid.UUID                      `json:"id" format:"uuid"`
	CreatedAt              time.Time                      `json:"created_at" format:"date-time"`
	UpdatedAt              time.Time                      `json:"updated_at" format:"date-time"`
	InitiatorID            uuid.UUID                      `json:"initiator_id" format:"uuid"`
	SourceWorkspaceID      uuid.UUID                      `json:"source_workspace_id" format:"uuid"`
	DestinationWorkspaceID uuid.UUID                      `json:"destination_workspace_id" format:"uuid"`
	AllowSourceRunning     bool                           `json:"allow_source_running"`
	Volumes                []WorkspaceVolumeCopySelection `json:"volumes"`
	Status                 WorkspaceVolumeCopyStatus      `json:"status"`
	Error                  string                         `json:"error,omitempty"`
	StartedAt              *time.Time                     `json:"started_at,omitempty" format:"date-time"`
	CompletedAt            *time.Time                     `json:"completed_at,omitempty" format:"date-time"`
	SyncOf                 *uuid.UUID                     `json:"sync_of,omitempty" format:"uuid"`
}

func (c *Client) WorkspaceVolumeCopyVolumes(ctx context.Context, workspaceID uuid.UUID) ([]WorkspaceVolumeCopyVolume, error) {
	res, err := c.Request(ctx, http.MethodGet, "/api/v2/workspaces/"+workspaceID.String()+"/volume-copy-volumes", nil)
	if err != nil {
		return nil, xerrors.Errorf("execute request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, ReadBodyAsError(res)
	}
	var volumes []WorkspaceVolumeCopyVolume
	return volumes, json.NewDecoder(res.Body).Decode(&volumes)
}

func (c *Client) CreateWorkspaceVolumeCopy(ctx context.Context, sourceWorkspaceID uuid.UUID, req CreateWorkspaceVolumeCopyRequest) (WorkspaceVolumeCopyOperation, error) {
	res, err := c.Request(ctx, http.MethodPost, "/api/v2/workspaces/"+sourceWorkspaceID.String()+"/volume-copy-operations", req)
	if err != nil {
		return WorkspaceVolumeCopyOperation{}, xerrors.Errorf("execute request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return WorkspaceVolumeCopyOperation{}, ReadBodyAsError(res)
	}
	var operation WorkspaceVolumeCopyOperation
	return operation, json.NewDecoder(res.Body).Decode(&operation)
}

func (c *Client) WorkspaceVolumeCopyOperation(ctx context.Context, operationID uuid.UUID) (WorkspaceVolumeCopyOperation, error) {
	res, err := c.Request(ctx, http.MethodGet, "/api/v2/workspace-volume-copies/"+operationID.String(), nil)
	if err != nil {
		return WorkspaceVolumeCopyOperation{}, xerrors.Errorf("execute request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return WorkspaceVolumeCopyOperation{}, ReadBodyAsError(res)
	}
	var operation WorkspaceVolumeCopyOperation
	return operation, json.NewDecoder(res.Body).Decode(&operation)
}

func (c *Client) SyncWorkspaceVolumeCopy(ctx context.Context, operationID uuid.UUID) (WorkspaceVolumeCopyOperation, error) {
	res, err := c.Request(ctx, http.MethodPost, "/api/v2/workspace-volume-copies/"+operationID.String()+"/sync", nil)
	if err != nil {
		return WorkspaceVolumeCopyOperation{}, xerrors.Errorf("execute request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		return WorkspaceVolumeCopyOperation{}, ReadBodyAsError(res)
	}
	var operation WorkspaceVolumeCopyOperation
	return operation, json.NewDecoder(res.Body).Decode(&operation)
}
