package coderd

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	databasepubsub "github.com/coder/coder/v2/coderd/database/pubsub"
	"github.com/coder/coder/v2/coderd/mcp"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/codersdk/toolsdk"
)

type workspaceMCPToolActivityRecorder struct {
	db           database.Store
	publisher    databasepubsub.Publisher
	logger       slog.Logger
	client       *codersdk.Client
	replicaID    uuid.UUID
	historyLimit int32
}

func newWorkspaceMCPToolActivityRecorder(
	db database.Store,
	publisher databasepubsub.Publisher,
	logger slog.Logger,
	client *codersdk.Client,
	replicaID uuid.UUID,
	historyLimit int64,
) *workspaceMCPToolActivityRecorder {
	boundedHistoryLimit := historyLimit
	if boundedHistoryLimit < 0 {
		boundedHistoryLimit = 0
	}
	if boundedHistoryLimit > math.MaxInt32 {
		boundedHistoryLimit = math.MaxInt32
	}
	return &workspaceMCPToolActivityRecorder{
		db:           db,
		publisher:    publisher,
		logger:       logger.Named("workspace-mcp-tool-activity"),
		client:       client,
		replicaID:    replicaID,
		historyLimit: int32(boundedHistoryLimit), // #nosec G115 -- clamped to MaxInt32 above.
	}
}

func (r *workspaceMCPToolActivityRecorder) StartToolActivity(
	ctx context.Context,
	_ string,
	toolName string,
	workspaceInput string,
	input string,
	correlationHash string,
	startedAt time.Time,
	persistTool bool,
) (mcp.PersistentActivityHandle, error) {
	if r == nil || r.client == nil || strings.TrimSpace(workspaceInput) == "" {
		return mcp.PersistentActivityHandle{}, nil
	}

	workspace, err := r.resolveWorkspace(ctx, workspaceInput)
	if err != nil {
		return mcp.PersistentActivityHandle{}, err
	}

	activityID := uuid.New()
	activityCtx := dbauthz.AsSystemRestricted(context.WithoutCancel(ctx))
	if err := r.db.InTx(func(tx database.Store) error {
		if err := tx.InsertWorkspaceMCPRequestActivity(activityCtx, database.InsertWorkspaceMCPRequestActivityParams{
			ID:              activityID,
			WorkspaceID:     workspace.ID,
			ReplicaID:       r.replicaID,
			Tool:            toolName,
			Input:           input,
			CorrelationHash: correlationHash,
			StartedAt:       startedAt,
		}); err != nil {
			return xerrors.Errorf("insert workspace MCP request activity: %w", err)
		}
		if !persistTool {
			return nil
		}
		if err := tx.InsertWorkspaceToolActivity(activityCtx, database.InsertWorkspaceToolActivityParams{
			ID:          activityID,
			WorkspaceID: workspace.ID,
			Tool:        toolName,
			Command:     input,
			StartedAt:   startedAt,
		}); err != nil {
			return xerrors.Errorf("insert workspace MCP tool activity: %w", err)
		}
		return nil
	}, nil); err != nil {
		return mcp.PersistentActivityHandle{}, err
	}

	r.publishRequestChanged(ctx, workspace.ID, activityID)
	if persistTool {
		r.publishChanged(ctx, workspace.ID, activityID)
	}
	return mcp.PersistentActivityHandle{ID: activityID, WorkspaceID: workspace.ID, PersistTool: persistTool}, nil
}

func (r *workspaceMCPToolActivityRecorder) HeartbeatToolActivity(
	ctx context.Context,
	handle mcp.PersistentActivityHandle,
	heartbeatAt time.Time,
) error {
	if r == nil || handle.ID == uuid.Nil || handle.WorkspaceID == uuid.Nil {
		return nil
	}
	updated, err := r.db.HeartbeatWorkspaceMCPRequestActivity(
		dbauthz.AsSystemRestricted(context.WithoutCancel(ctx)),
		database.HeartbeatWorkspaceMCPRequestActivityParams{
			HeartbeatAt: heartbeatAt,
			ID:          handle.ID,
			WorkspaceID: handle.WorkspaceID,
		},
	)
	if err != nil {
		return xerrors.Errorf("heartbeat workspace MCP request activity: %w", err)
	}
	if updated == 0 {
		return xerrors.Errorf("heartbeat workspace MCP request activity: row %s is no longer running", handle.ID)
	}
	return nil
}

func (r *workspaceMCPToolActivityRecorder) FinishToolActivity(
	ctx context.Context,
	handle mcp.PersistentActivityHandle,
	status mcp.PersistentActivityStatus,
	output string,
	finishedAt time.Time,
) error {
	if r == nil || handle.ID == uuid.Nil || handle.WorkspaceID == uuid.Nil {
		return nil
	}

	activityCtx := dbauthz.AsSystemRestricted(context.WithoutCancel(ctx))
	var commandPruned bool
	if err := r.db.InTx(func(tx database.Store) error {
		updated, err := tx.FinishWorkspaceMCPRequestActivity(activityCtx, database.FinishWorkspaceMCPRequestActivityParams{
			Status:      string(status),
			FinishedAt:  sql.NullTime{Time: finishedAt, Valid: true},
			ID:          handle.ID,
			WorkspaceID: handle.WorkspaceID,
		})
		if err != nil {
			return xerrors.Errorf("finish workspace MCP request activity: %w", err)
		}
		if updated == 0 {
			return xerrors.Errorf("finish workspace MCP request activity: row %s is no longer running", handle.ID)
		}

		if handle.PersistTool {
			updated, err := tx.FinishWorkspaceToolActivity(activityCtx, database.FinishWorkspaceToolActivityParams{
				Status:      string(status),
				FinishedAt:  sql.NullTime{Time: finishedAt, Valid: true},
				ID:          handle.ID,
				WorkspaceID: handle.WorkspaceID,
				Output:      output,
			})
			if err != nil {
				return xerrors.Errorf("finish workspace MCP tool activity: %w", err)
			}
			if updated == 0 {
				return xerrors.Errorf("finish workspace MCP tool activity: row %s is no longer running", handle.ID)
			}
		}

		if r.historyLimit <= 0 {
			return nil
		}
		pruned, err := tx.PruneWorkspaceCommandActivity(activityCtx, database.PruneWorkspaceCommandActivityParams{
			WorkspaceID:  handle.WorkspaceID,
			HistoryLimit: r.historyLimit,
		})
		if err != nil {
			return xerrors.Errorf("prune workspace activity: %w", err)
		}
		commandPruned = pruned > 0
		if _, err := tx.PruneWorkspaceMCPRequestActivity(activityCtx, database.PruneWorkspaceMCPRequestActivityParams{
			WorkspaceID:  handle.WorkspaceID,
			HistoryLimit: r.historyLimit,
		}); err != nil {
			return xerrors.Errorf("prune workspace MCP request activity: %w", err)
		}
		return nil
	}, nil); err != nil {
		return err
	}

	r.publishRequestChanged(ctx, handle.WorkspaceID, handle.ID)
	if handle.PersistTool {
		r.publishChanged(ctx, handle.WorkspaceID, handle.ID)
	}
	if commandPruned {
		r.publishResync(ctx, handle.WorkspaceID)
	}
	return nil
}

func (r *workspaceMCPToolActivityRecorder) resolveWorkspace(ctx context.Context, input string) (codersdk.Workspace, error) {
	normalized := toolsdk.NormalizeWorkspaceInput(strings.TrimSpace(input))
	workspaceInput := normalized
	if _, err := uuid.Parse(normalized); err != nil {
		if workspace, _, found := strings.Cut(normalized, "."); found {
			workspaceInput = workspace
		}
	}

	workspace, err := r.client.ResolveWorkspace(ctx, workspaceInput)
	if err == nil {
		return workspace, nil
	}
	if !workspaceActivitySDKNotFound(err) || strings.Contains(workspaceInput, "/") {
		return codersdk.Workspace{}, xerrors.Errorf("resolve workspace %q: %w", workspaceInput, err)
	}

	visible, listErr := r.client.Workspaces(ctx, codersdk.WorkspaceFilter{})
	if listErr != nil {
		return codersdk.Workspace{}, xerrors.Errorf("list accessible workspaces: %w", listErr)
	}

	var match *codersdk.Workspace
	for i := range visible.Workspaces {
		candidate := &visible.Workspaces[i]
		if !strings.EqualFold(candidate.Name, workspaceInput) {
			continue
		}
		if match != nil {
			return codersdk.Workspace{}, xerrors.Errorf("workspace name %q is ambiguous; use owner/workspace", workspaceInput)
		}
		match = candidate
	}
	if match == nil {
		return codersdk.Workspace{}, xerrors.Errorf("workspace %q is not accessible", workspaceInput)
	}
	return *match, nil
}

func workspaceActivitySDKNotFound(err error) bool {
	var sdkErr *codersdk.Error
	return errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound
}

func (r *workspaceMCPToolActivityRecorder) publishRequestChanged(ctx context.Context, workspaceID, requestID uuid.UUID) {
	if err := coderdpubsub.PublishWorkspaceActivityEvent(r.publisher, workspaceID, coderdpubsub.WorkspaceActivityEvent{
		Type:      coderdpubsub.WorkspaceActivityEventMCPRequestChanged,
		RequestID: requestID,
	}); err != nil {
		r.logger.Debug(ctx, "publish MCP request activity change", slog.Error(err), slog.F("workspace_id", workspaceID), slog.F("request_id", requestID))
	}
}

func (r *workspaceMCPToolActivityRecorder) publishChanged(ctx context.Context, workspaceID, activityID uuid.UUID) {
	if err := coderdpubsub.PublishWorkspaceActivityEvent(r.publisher, workspaceID, coderdpubsub.WorkspaceActivityEvent{
		Type:      coderdpubsub.WorkspaceActivityEventCommandChanged,
		CommandID: activityID,
	}); err != nil {
		r.logger.Debug(ctx, "publish MCP tool activity change", slog.Error(err), slog.F("workspace_id", workspaceID), slog.F("activity_id", activityID))
	}
}

func (r *workspaceMCPToolActivityRecorder) publishResync(ctx context.Context, workspaceID uuid.UUID) {
	if err := coderdpubsub.PublishWorkspaceActivityEvent(r.publisher, workspaceID, coderdpubsub.WorkspaceActivityEvent{
		Type: coderdpubsub.WorkspaceActivityEventCommandResync,
	}); err != nil {
		r.logger.Debug(ctx, "publish MCP tool activity resync", slog.Error(err), slog.F("workspace_id", workspaceID))
	}
}
