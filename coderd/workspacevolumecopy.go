package coderd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/audit"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	"github.com/coder/coder/v2/coderd/database/dbtime"
	"github.com/coder/coder/v2/coderd/httpapi"
	"github.com/coder/coder/v2/coderd/httpapi/httperror"
	"github.com/coder/coder/v2/coderd/httpmw"
	"github.com/coder/coder/v2/coderd/rbac/policy"
	volcopyk8s "github.com/coder/coder/v2/coderd/workspacevolumecopy"
	"github.com/coder/coder/v2/codersdk"
)

const workspaceVolumeCopyReconcileInterval = 2 * time.Second

var workspaceVolumeCopyProtectedPaths = []string{
	".ssh/id_ed25519_workspace",
	".ssh/id_ed25519_workspace.pub",
	".ssh/config.d/00-coder-workspace-github.conf",
	".local/state/coder-command-activity",
	".local/state/coder",
	".developer-workspace-seeded",
}

type resolvedWorkspaceVolumeCopy struct {
	Key                 string   `json:"key"`
	Overwrite           bool     `json:"overwrite"`
	SourceClaim         string   `json:"source_claim"`
	DestinationClaim    string   `json:"destination_claim"`
	ExcludedPaths       []string `json:"excluded_paths,omitempty"`
	SourceOwnerUID      *uint32  `json:"source_owner_uid,omitempty"`
	SourceOwnerGID      *uint32  `json:"source_owner_gid,omitempty"`
	DestinationOwnerUID *uint32  `json:"destination_owner_uid,omitempty"`
	DestinationOwnerGID *uint32  `json:"destination_owner_gid,omitempty"`
}

type workspaceVolumeCopyAuditFields struct {
	OperationID            uuid.UUID                               `json:"volume_copy_operation_id"`
	SourceWorkspaceID      uuid.UUID                               `json:"source_workspace_id"`
	DestinationWorkspaceID uuid.UUID                               `json:"destination_workspace_id"`
	AllowSourceRunning     bool                                    `json:"allow_source_running"`
	Volumes                []codersdk.WorkspaceVolumeCopySelection `json:"volumes"`
	SyncOf                 uuid.NullUUID                           `json:"sync_of,omitempty"`
}

// @Summary List copyable persistent volumes for a workspace
// @ID get-workspace-volume-copy-volumes
// @Security CoderSessionToken
// @Produce json
// @Tags Workspaces
// @Param workspace path string true "Workspace ID" format(uuid)
// @Success 200 {array} codersdk.WorkspaceVolumeCopyVolume
// @Router /api/v2/workspaces/{workspace}/volume-copy-volumes [get]
func (api *API) workspaceVolumeCopyVolumes(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspace := httpmw.WorkspaceParam(r)
	if !api.workspaceVolumeCopyEnabled(rw) {
		return
	}
	if !api.Authorize(r, policy.ActionWorkspaceVolumeCopy, workspace) {
		httpapi.Forbidden(rw)
		return
	}

	volumes, err := api.workspaceVolumeCopyKubernetes.ListWorkspaceVolumes(
		ctx,
		api.DeploymentValues.WorkspaceVolumeCopyNamespace.String(),
		workspace.ID,
	)
	if err != nil {
		httpapi.Write(ctx, rw, http.StatusBadGateway, codersdk.Response{
			Message: "Failed to discover workspace persistent volumes.",
			Detail:  err.Error(),
		})
		return
	}
	response := make([]codersdk.WorkspaceVolumeCopyVolume, 0, len(volumes))
	for _, volume := range volumes {
		response = append(response, codersdk.WorkspaceVolumeCopyVolume{
			Key:           volume.Key,
			DisplayName:   volume.DisplayName,
			MountPath:     volume.MountPath,
			Capacity:      volume.Capacity,
			ExcludedPaths: mergeWorkspaceVolumeCopyExcludes(volume.ExcludedPaths),
		})
	}
	httpapi.Write(ctx, rw, http.StatusOK, response)
}

// @Summary Create a workspace persistent-volume copy operation
// @ID create-workspace-volume-copy
// @Security CoderSessionToken
// @Accept json
// @Produce json
// @Tags Workspaces
// @Param workspace path string true "Source workspace ID" format(uuid)
// @Param request body codersdk.CreateWorkspaceVolumeCopyRequest true "Copy request"
// @Success 201 {object} codersdk.WorkspaceVolumeCopyOperation
// @Router /api/v2/workspaces/{workspace}/volume-copy-operations [post]
func (api *API) postWorkspaceVolumeCopyOperation(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !api.workspaceVolumeCopyEnabled(rw) {
		return
	}
	source := httpmw.WorkspaceParam(r)
	var req codersdk.CreateWorkspaceVolumeCopyRequest
	if !httpapi.Read(ctx, rw, r, &req) {
		return
	}
	if req.DestinationWorkspaceID == uuid.Nil {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "Destination workspace is required."})
		return
	}
	if req.DestinationWorkspaceID == source.ID {
		httpapi.Write(ctx, rw, http.StatusBadRequest, codersdk.Response{Message: "Source and destination workspaces must differ."})
		return
	}

	destination, err := api.Database.GetWorkspaceByID(ctx, req.DestinationWorkspaceID)
	if xerrors.Is(err, sql.ErrNoRows) {
		httpapi.ResourceNotFound(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	if !api.authorizeWorkspaceVolumeCopyPair(r, source, destination) {
		httpapi.Forbidden(rw)
		return
	}

	auditFields := &workspaceVolumeCopyAuditFields{
		SourceWorkspaceID:      source.ID,
		DestinationWorkspaceID: destination.ID,
		AllowSourceRunning:     req.AllowSourceRunning,
		Volumes:                req.Volumes,
	}
	aReq, commitAudit := audit.InitRequest[database.WorkspaceTable](rw, &audit.RequestParams{
		Audit:            *api.Auditor.Load(),
		Log:              api.Logger,
		Request:          r,
		Action:           database.AuditActionWrite,
		AdditionalFields: auditFields,
		OrganizationID:   source.OrganizationID,
	})
	aReq.Old = source.WorkspaceTable()
	aReq.New = source.WorkspaceTable()
	defer commitAudit()

	operation, err := api.createWorkspaceVolumeCopyOperation(
		ctx,
		httpmw.APIKey(r).UserID,
		source,
		destination,
		req.AllowSourceRunning,
		req.Volumes,
		uuid.NullUUID{},
	)
	if err != nil {
		httperror.WriteResponseError(ctx, rw, err)
		return
	}
	auditFields.OperationID = operation.ID
	httpapi.Write(ctx, rw, http.StatusCreated, operation)
}

// @Summary Get a workspace persistent-volume copy operation
// @ID get-workspace-volume-copy
// @Security CoderSessionToken
// @Produce json
// @Tags Workspaces
// @Param operation path string true "Volume copy operation ID" format(uuid)
// @Success 200 {object} codersdk.WorkspaceVolumeCopyOperation
// @Router /api/v2/workspace-volume-copies/{operation} [get]
func (api *API) workspaceVolumeCopyOperation(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !api.workspaceVolumeCopyEnabled(rw) {
		return
	}
	operationID, ok := parseWorkspaceVolumeCopyOperationID(rw, r)
	if !ok {
		return
	}
	operation, err := api.Database.GetWorkspaceVolumeCopyOperationByID(
		dbauthz.AsWorkspaceVolumeCopy(ctx), operationID,
	)
	if xerrors.Is(err, sql.ErrNoRows) {
		httpapi.ResourceNotFound(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	if !api.authorizeWorkspaceVolumeCopyOperation(rw, r, operation) {
		return
	}
	response, err := workspaceVolumeCopyOperationToSDK(operation)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	httpapi.Write(ctx, rw, http.StatusOK, response)
}

// @Summary Synchronize a completed workspace persistent-volume copy operation again
// @ID sync-workspace-volume-copy
// @Security CoderSessionToken
// @Produce json
// @Tags Workspaces
// @Param operation path string true "Volume copy operation ID" format(uuid)
// @Success 201 {object} codersdk.WorkspaceVolumeCopyOperation
// @Router /api/v2/workspace-volume-copies/{operation}/sync [post]
func (api *API) postWorkspaceVolumeCopySync(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if !api.workspaceVolumeCopyEnabled(rw) {
		return
	}
	operationID, ok := parseWorkspaceVolumeCopyOperationID(rw, r)
	if !ok {
		return
	}
	operation, err := api.Database.GetWorkspaceVolumeCopyOperationByID(
		dbauthz.AsWorkspaceVolumeCopy(ctx), operationID,
	)
	if xerrors.Is(err, sql.ErrNoRows) {
		httpapi.ResourceNotFound(rw)
		return
	}
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	if operation.Status != string(codersdk.WorkspaceVolumeCopyStatusSucceeded) {
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "Only a completed volume copy operation can be synchronized again.",
		})
		return
	}
	if !operation.AllowSourceRunning {
		httpapi.Write(ctx, rw, http.StatusConflict, codersdk.Response{
			Message: "Only a completed live volume copy operation can be synchronized again.",
		})
		return
	}

	source, destination, ok := api.workspaceVolumeCopyOperationWorkspaces(rw, r, operation)
	if !ok {
		return
	}
	resolved, err := decodeResolvedWorkspaceVolumeCopies(operation.Volumes)
	if err != nil {
		httpapi.InternalServerError(rw, err)
		return
	}
	selections := make([]codersdk.WorkspaceVolumeCopySelection, 0, len(resolved))
	for _, volume := range resolved {
		selections = append(selections, codersdk.WorkspaceVolumeCopySelection{
			Key:       volume.Key,
			Overwrite: volume.Overwrite,
		})
	}

	auditFields := &workspaceVolumeCopyAuditFields{
		SourceWorkspaceID:      source.ID,
		DestinationWorkspaceID: destination.ID,
		AllowSourceRunning:     operation.AllowSourceRunning,
		Volumes:                selections,
		SyncOf:                 uuid.NullUUID{UUID: operation.ID, Valid: true},
	}
	aReq, commitAudit := audit.InitRequest[database.WorkspaceTable](rw, &audit.RequestParams{
		Audit:            *api.Auditor.Load(),
		Log:              api.Logger,
		Request:          r,
		Action:           database.AuditActionWrite,
		AdditionalFields: auditFields,
		OrganizationID:   source.OrganizationID,
	})
	aReq.Old = source.WorkspaceTable()
	aReq.New = source.WorkspaceTable()
	defer commitAudit()

	newOperation, err := api.createWorkspaceVolumeCopyOperation(
		ctx,
		httpmw.APIKey(r).UserID,
		source,
		destination,
		operation.AllowSourceRunning,
		selections,
		uuid.NullUUID{UUID: operation.ID, Valid: true},
	)
	if err != nil {
		httperror.WriteResponseError(ctx, rw, err)
		return
	}
	auditFields.OperationID = newOperation.ID
	httpapi.Write(ctx, rw, http.StatusCreated, newOperation)
}

func (api *API) createWorkspaceVolumeCopyOperation(
	ctx context.Context,
	initiatorID uuid.UUID,
	source database.Workspace,
	destination database.Workspace,
	allowSourceRunning bool,
	selections []codersdk.WorkspaceVolumeCopySelection,
	syncOf uuid.NullUUID,
) (codersdk.WorkspaceVolumeCopyOperation, error) {
	resolved, err := api.resolveWorkspaceVolumeCopySelections(ctx, source.ID, destination.ID, selections)
	if err != nil {
		return codersdk.WorkspaceVolumeCopyOperation{}, err
	}
	resolvedJSON, err := json.Marshal(resolved)
	if err != nil {
		return codersdk.WorkspaceVolumeCopyOperation{}, httperror.NewResponseError(http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to encode volume copy plan.", Detail: err.Error(),
		})
	}

	operationID := uuid.New()
	now := dbtime.Now()
	jobName := workspaceVolumeCopyJobName(operationID)
	var operation database.WorkspaceVolumeCopyOperation
	err = database.ReadModifyUpdate(api.Database, func(tx database.Store) error {
		internalCtx := dbauthz.AsWorkspaceVolumeCopy(ctx)
		workspaceIDs := []uuid.UUID{source.ID, destination.ID}
		slices.SortFunc(workspaceIDs, func(a, b uuid.UUID) int {
			return strings.Compare(a.String(), b.String())
		})
		for _, workspaceID := range workspaceIDs {
			if _, err := tx.AcquireWorkspaceVolumeCopyLifecycleLock(internalCtx, workspaceID); err != nil {
				return httperror.NewResponseError(http.StatusInternalServerError, codersdk.Response{
					Message: "Failed to lock workspace lifecycle for volume copy.", Detail: err.Error(),
				})
			}
		}
		for _, workspaceID := range workspaceIDs {
			lock, err := tx.GetWorkspaceVolumeCopyLockByWorkspaceID(internalCtx, workspaceID)
			if err == nil {
				return httperror.NewResponseError(http.StatusConflict, codersdk.Response{
					Message: fmt.Sprintf("Workspace lifecycle is already locked by volume copy operation %s.", lock.OperationID),
				})
			}
			if !xerrors.Is(err, sql.ErrNoRows) {
				return httperror.NewResponseError(http.StatusInternalServerError, codersdk.Response{
					Message: "Failed to inspect workspace volume-copy lock.", Detail: err.Error(),
				})
			}
		}

		sourceStatus, err := workspaceVolumeCopyWorkspaceStatus(ctx, tx, source.ID)
		if err != nil {
			return err
		}
		destinationStatus, err := workspaceVolumeCopyWorkspaceStatus(ctx, tx, destination.ID)
		if err != nil {
			return err
		}
		if destinationStatus != codersdk.WorkspaceStatusStopped {
			return httperror.NewResponseError(http.StatusConflict, codersdk.Response{
				Message: fmt.Sprintf("Destination workspace must be stopped before copying volumes; current status is %s.", destinationStatus),
			})
		}
		if allowSourceRunning {
			if sourceStatus != codersdk.WorkspaceStatusStopped && sourceStatus != codersdk.WorkspaceStatusRunning {
				return httperror.NewResponseError(http.StatusConflict, codersdk.Response{
					Message: fmt.Sprintf("Source workspace must be stopped or running before copying volumes; current status is %s.", sourceStatus),
				})
			}
		} else if sourceStatus != codersdk.WorkspaceStatusStopped {
			return httperror.NewResponseError(http.StatusConflict, codersdk.Response{
				Message: fmt.Sprintf("Source workspace must be stopped before copying volumes; current status is %s. Stop it or enable copying while the source is running.", sourceStatus),
			})
		}

		operation, err = tx.InsertWorkspaceVolumeCopyOperation(internalCtx, database.InsertWorkspaceVolumeCopyOperationParams{
			ID:                     operationID,
			CreatedAt:              now,
			UpdatedAt:              now,
			InitiatorID:            initiatorID,
			SourceWorkspaceID:      source.ID,
			DestinationWorkspaceID: destination.ID,
			AllowSourceRunning:     allowSourceRunning,
			Volumes:                resolvedJSON,
			Status:                 string(codersdk.WorkspaceVolumeCopyStatusPending),
			Namespace:              api.DeploymentValues.WorkspaceVolumeCopyNamespace.String(),
			JobName:                jobName,
			Error:                  "",
			SyncOf:                 syncOf,
		})
		if err != nil {
			return httperror.NewResponseError(http.StatusInternalServerError, codersdk.Response{
				Message: "Failed to create workspace volume copy operation.", Detail: err.Error(),
			})
		}
		for _, workspaceID := range workspaceIDs {
			if err := tx.InsertWorkspaceVolumeCopyLock(internalCtx, database.InsertWorkspaceVolumeCopyLockParams{
				WorkspaceID: workspaceID,
				OperationID: operationID,
				CreatedAt:   now,
			}); err != nil {
				return httperror.NewResponseError(http.StatusInternalServerError, codersdk.Response{
					Message: "Failed to create workspace lifecycle lock.", Detail: err.Error(),
				})
			}
		}
		return nil
	})
	if err != nil {
		return codersdk.WorkspaceVolumeCopyOperation{}, err
	}
	api.Logger.Info(ctx, "workspace volume copy queued",
		slog.F("operation_id", operation.ID),
		slog.F("source_workspace_id", source.ID),
		slog.F("destination_workspace_id", destination.ID),
		slog.F("allow_source_running", allowSourceRunning),
		slog.F("volume_count", len(resolved)),
	)
	return workspaceVolumeCopyOperationToSDK(operation)
}

func (api *API) resolveWorkspaceVolumeCopySelections(
	ctx context.Context,
	sourceWorkspaceID uuid.UUID,
	destinationWorkspaceID uuid.UUID,
	selections []codersdk.WorkspaceVolumeCopySelection,
) ([]resolvedWorkspaceVolumeCopy, error) {
	if len(selections) == 0 {
		return nil, httperror.NewResponseError(http.StatusBadRequest, codersdk.Response{Message: "Select at least one persistent volume to copy."})
	}
	namespace := api.DeploymentValues.WorkspaceVolumeCopyNamespace.String()
	sourceVolumes, err := api.workspaceVolumeCopyKubernetes.ListWorkspaceVolumes(ctx, namespace, sourceWorkspaceID)
	if err != nil {
		return nil, httperror.NewResponseError(http.StatusBadGateway, codersdk.Response{Message: "Failed to discover source workspace volumes.", Detail: err.Error()})
	}
	destinationVolumes, err := api.workspaceVolumeCopyKubernetes.ListWorkspaceVolumes(ctx, namespace, destinationWorkspaceID)
	if err != nil {
		return nil, httperror.NewResponseError(http.StatusBadGateway, codersdk.Response{Message: "Failed to discover destination workspace volumes.", Detail: err.Error()})
	}
	sourceByKey := make(map[string]volcopyk8s.Volume, len(sourceVolumes))
	for _, volume := range sourceVolumes {
		sourceByKey[volume.Key] = volume
	}
	destinationByKey := make(map[string]volcopyk8s.Volume, len(destinationVolumes))
	for _, volume := range destinationVolumes {
		destinationByKey[volume.Key] = volume
	}

	seen := make(map[string]struct{}, len(selections))
	resolved := make([]resolvedWorkspaceVolumeCopy, 0, len(selections))
	for _, selection := range selections {
		key := strings.TrimSpace(selection.Key)
		if key == "" {
			return nil, httperror.NewResponseError(http.StatusBadRequest, codersdk.Response{Message: "Volume key must not be empty."})
		}
		if _, ok := seen[key]; ok {
			return nil, httperror.NewResponseError(http.StatusBadRequest, codersdk.Response{Message: fmt.Sprintf("Volume %q was selected more than once.", key)})
		}
		seen[key] = struct{}{}
		sourceVolume, ok := sourceByKey[key]
		if !ok {
			return nil, httperror.NewResponseError(http.StatusBadRequest, codersdk.Response{Message: fmt.Sprintf("Source workspace does not expose a copyable volume with key %q.", key)})
		}
		destinationVolume, ok := destinationByKey[key]
		if !ok {
			return nil, httperror.NewResponseError(http.StatusBadRequest, codersdk.Response{Message: fmt.Sprintf("Destination workspace does not expose a copyable volume with key %q.", key)})
		}
		resolved = append(resolved, resolvedWorkspaceVolumeCopy{
			Key:                 key,
			Overwrite:           selection.Overwrite,
			SourceClaim:         sourceVolume.ClaimName,
			DestinationClaim:    destinationVolume.ClaimName,
			SourceOwnerUID:      sourceVolume.OwnerUID,
			SourceOwnerGID:      sourceVolume.OwnerGID,
			DestinationOwnerUID: destinationVolume.OwnerUID,
			DestinationOwnerGID: destinationVolume.OwnerGID,
			ExcludedPaths: mergeWorkspaceVolumeCopyExcludes(
				sourceVolume.ExcludedPaths,
				destinationVolume.ExcludedPaths,
			),
		})
	}
	return resolved, nil
}

func workspaceVolumeCopyWorkspaceStatus(ctx context.Context, tx database.Store, workspaceID uuid.UUID) (codersdk.WorkspaceStatus, error) {
	build, err := tx.GetLatestWorkspaceBuildByWorkspaceID(ctx, workspaceID)
	if err != nil {
		return "", httperror.NewResponseError(http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to fetch workspace build state.", Detail: err.Error(),
		})
	}
	job, err := tx.GetProvisionerJobByID(ctx, build.JobID)
	if err != nil {
		return "", httperror.NewResponseError(http.StatusInternalServerError, codersdk.Response{
			Message: "Failed to fetch workspace job state.", Detail: err.Error(),
		})
	}
	return codersdk.ConvertWorkspaceStatus(codersdk.ProvisionerJobStatus(job.JobStatus), codersdk.WorkspaceTransition(build.Transition)), nil
}

func (api *API) authorizeWorkspaceVolumeCopyPair(r *http.Request, source, destination database.Workspace) bool {
	return api.Authorize(r, policy.ActionWorkspaceVolumeCopy, source) &&
		api.Authorize(r, policy.ActionWorkspaceVolumeCopy, destination)
}

func (api *API) authorizeWorkspaceVolumeCopyOperation(rw http.ResponseWriter, r *http.Request, operation database.WorkspaceVolumeCopyOperation) bool {
	_, _, ok := api.workspaceVolumeCopyOperationWorkspaces(rw, r, operation)
	return ok
}

func (api *API) workspaceVolumeCopyOperationWorkspaces(
	rw http.ResponseWriter,
	r *http.Request,
	operation database.WorkspaceVolumeCopyOperation,
) (database.Workspace, database.Workspace, bool) {
	ctx := r.Context()
	source, err := api.Database.GetWorkspaceByID(ctx, operation.SourceWorkspaceID)
	if err != nil {
		if xerrors.Is(err, sql.ErrNoRows) {
			httpapi.ResourceNotFound(rw)
		} else {
			httpapi.InternalServerError(rw, err)
		}
		return database.Workspace{}, database.Workspace{}, false
	}
	destination, err := api.Database.GetWorkspaceByID(ctx, operation.DestinationWorkspaceID)
	if err != nil {
		if xerrors.Is(err, sql.ErrNoRows) {
			httpapi.ResourceNotFound(rw)
		} else {
			httpapi.InternalServerError(rw, err)
		}
		return database.Workspace{}, database.Workspace{}, false
	}
	if !api.authorizeWorkspaceVolumeCopyPair(r, source, destination) {
		httpapi.Forbidden(rw)
		return database.Workspace{}, database.Workspace{}, false
	}
	return source, destination, true
}

func (api *API) workspaceVolumeCopyEnabled(rw http.ResponseWriter) bool {
	if !api.DeploymentValues.WorkspaceVolumeCopyEnabled.Value() || api.workspaceVolumeCopyKubernetes == nil {
		httpapi.ResourceNotFound(rw)
		return false
	}
	return true
}

func parseWorkspaceVolumeCopyOperationID(rw http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	operationID, err := uuid.Parse(chi.URLParam(r, "operation"))
	if err != nil {
		httpapi.Write(r.Context(), rw, http.StatusBadRequest, codersdk.Response{Message: "Volume copy operation ID must be a valid UUID."})
		return uuid.Nil, false
	}
	return operationID, true
}

func workspaceVolumeCopyOperationToSDK(operation database.WorkspaceVolumeCopyOperation) (codersdk.WorkspaceVolumeCopyOperation, error) {
	resolved, err := decodeResolvedWorkspaceVolumeCopies(operation.Volumes)
	if err != nil {
		return codersdk.WorkspaceVolumeCopyOperation{}, err
	}
	selections := make([]codersdk.WorkspaceVolumeCopySelection, 0, len(resolved))
	for _, volume := range resolved {
		selections = append(selections, codersdk.WorkspaceVolumeCopySelection{Key: volume.Key, Overwrite: volume.Overwrite})
	}
	response := codersdk.WorkspaceVolumeCopyOperation{
		ID:                     operation.ID,
		CreatedAt:              operation.CreatedAt,
		UpdatedAt:              operation.UpdatedAt,
		InitiatorID:            operation.InitiatorID,
		SourceWorkspaceID:      operation.SourceWorkspaceID,
		DestinationWorkspaceID: operation.DestinationWorkspaceID,
		AllowSourceRunning:     operation.AllowSourceRunning,
		Volumes:                selections,
		Status:                 codersdk.WorkspaceVolumeCopyStatus(operation.Status),
		Error:                  operation.Error,
	}
	if operation.StartedAt.Valid {
		startedAt := operation.StartedAt.Time
		response.StartedAt = &startedAt
	}
	if operation.CompletedAt.Valid {
		completedAt := operation.CompletedAt.Time
		response.CompletedAt = &completedAt
	}
	if operation.SyncOf.Valid {
		syncOf := operation.SyncOf.UUID
		response.SyncOf = &syncOf
	}
	return response, nil
}

func decodeResolvedWorkspaceVolumeCopies(raw json.RawMessage) ([]resolvedWorkspaceVolumeCopy, error) {
	var resolved []resolvedWorkspaceVolumeCopy
	if err := json.Unmarshal(raw, &resolved); err != nil {
		return nil, xerrors.Errorf("decode workspace volume copy plan: %w", err)
	}
	if len(resolved) == 0 {
		return nil, errors.New("workspace volume copy plan contains no volumes")
	}
	return resolved, nil
}

func mergeWorkspaceVolumeCopyExcludes(pathSets ...[]string) []string {
	seen := make(map[string]struct{})
	merged := make([]string, 0, len(workspaceVolumeCopyProtectedPaths))
	appendPaths := func(paths []string) {
		for _, path := range paths {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			merged = append(merged, path)
		}
	}
	appendPaths(workspaceVolumeCopyProtectedPaths)
	for _, paths := range pathSets {
		appendPaths(paths)
	}
	slices.Sort(merged)
	return merged
}

func workspaceVolumeCopyJobName(operationID uuid.UUID) string {
	compact := strings.ReplaceAll(operationID.String(), "-", "")
	return "coder-volume-copy-" + compact[:20]
}

func (api *API) reconcileWorkspaceVolumeCopies() {
	ticker := time.NewTicker(workspaceVolumeCopyReconcileInterval)
	defer ticker.Stop()
	for {
		if err := api.reconcileWorkspaceVolumeCopiesOnce(api.ctx); err != nil && !errors.Is(err, context.Canceled) {
			api.Logger.Warn(api.ctx, "workspace volume copy reconciliation failed", slog.Error(err))
		}
		select {
		case <-api.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (api *API) reconcileWorkspaceVolumeCopiesOnce(ctx context.Context) error {
	internalCtx := dbauthz.AsWorkspaceVolumeCopy(ctx)
	operations, err := api.Database.GetActiveWorkspaceVolumeCopyOperations(internalCtx)
	if err != nil {
		return err
	}
	for _, operation := range operations {
		if err := api.reconcileWorkspaceVolumeCopy(ctx, operation); err != nil {
			api.Logger.Warn(ctx, "workspace volume copy operation reconciliation failed",
				slog.F("operation_id", operation.ID), slog.Error(err))
		}
	}
	return nil
}

func (api *API) reconcileWorkspaceVolumeCopy(ctx context.Context, operation database.WorkspaceVolumeCopyOperation) error {
	resolved, err := decodeResolvedWorkspaceVolumeCopies(operation.Volumes)
	if err != nil {
		return api.failWorkspaceVolumeCopy(ctx, operation.ID, "Invalid persisted volume copy plan: "+err.Error())
	}
	jobVolumes := make([]volcopyk8s.JobVolume, 0, len(resolved))
	for _, volume := range resolved {
		jobVolumes = append(jobVolumes, volcopyk8s.JobVolume{
			Key:                 volume.Key,
			SourceClaim:         volume.SourceClaim,
			DestinationClaim:    volume.DestinationClaim,
			Overwrite:           volume.Overwrite,
			ExcludedPaths:       volume.ExcludedPaths,
			SourceOwnerUID:      volume.SourceOwnerUID,
			SourceOwnerGID:      volume.SourceOwnerGID,
			DestinationOwnerUID: volume.DestinationOwnerUID,
			DestinationOwnerGID: volume.DestinationOwnerGID,
		})
	}

	if operation.Status == string(codersdk.WorkspaceVolumeCopyStatusPending) {
		err := api.workspaceVolumeCopyKubernetes.EnsureCopyJob(
			ctx,
			operation.Namespace,
			operation.JobName,
			api.DeploymentValues.WorkspaceVolumeCopyImage.String(),
			operation.ID,
			operation.AllowSourceRunning,
			jobVolumes,
		)
		if err != nil {
			var apiErr *volcopyk8s.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 && apiErr.StatusCode != http.StatusTooManyRequests {
				return api.failWorkspaceVolumeCopy(ctx, operation.ID, "Kubernetes rejected the volume copy Job: "+err.Error())
			}
			return err
		}
		now := dbtime.Now()
		_, err = api.Database.MarkWorkspaceVolumeCopyOperationRunning(
			dbauthz.AsWorkspaceVolumeCopy(ctx),
			database.MarkWorkspaceVolumeCopyOperationRunningParams{
				UpdatedAt: now,
				StartedAt: sql.NullTime{Time: now, Valid: true},
				ID:        operation.ID,
			},
		)
		return err
	}

	state, err := api.workspaceVolumeCopyKubernetes.GetCopyJobState(ctx, operation.Namespace, operation.JobName)
	if errors.Is(err, volcopyk8s.ErrNotFound) {
		return api.failWorkspaceVolumeCopy(ctx, operation.ID, "Kubernetes volume copy Job disappeared before completion.")
	}
	if err != nil {
		return err
	}
	if state.Failed {
		message := strings.TrimSpace(state.Message)
		if message == "" {
			message = "Kubernetes volume copy Job failed."
		}
		return api.failWorkspaceVolumeCopy(ctx, operation.ID, message)
	}
	if !state.Succeeded {
		return nil
	}
	return api.succeedWorkspaceVolumeCopy(ctx, operation.ID)
}

func (api *API) succeedWorkspaceVolumeCopy(ctx context.Context, operationID uuid.UUID) error {
	now := dbtime.Now()
	internalCtx := dbauthz.AsWorkspaceVolumeCopy(ctx)
	return api.Database.InTx(func(tx database.Store) error {
		if _, err := tx.MarkWorkspaceVolumeCopyOperationSucceeded(internalCtx, database.MarkWorkspaceVolumeCopyOperationSucceededParams{
			UpdatedAt:   now,
			CompletedAt: sql.NullTime{Time: now, Valid: true},
			ID:          operationID,
		}); err != nil {
			return err
		}
		return tx.DeleteWorkspaceVolumeCopyLocksByOperationID(internalCtx, operationID)
	}, nil)
}

func (api *API) failWorkspaceVolumeCopy(ctx context.Context, operationID uuid.UUID, message string) error {
	now := dbtime.Now()
	internalCtx := dbauthz.AsWorkspaceVolumeCopy(ctx)
	return api.Database.InTx(func(tx database.Store) error {
		if _, err := tx.MarkWorkspaceVolumeCopyOperationFailed(internalCtx, database.MarkWorkspaceVolumeCopyOperationFailedParams{
			UpdatedAt:   now,
			CompletedAt: sql.NullTime{Time: now, Valid: true},
			Error:       message,
			ID:          operationID,
		}); err != nil {
			return err
		}
		return tx.DeleteWorkspaceVolumeCopyLocksByOperationID(internalCtx, operationID)
	}, nil)
}
