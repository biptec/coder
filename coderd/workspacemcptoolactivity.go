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
	historyLimit int32
}

func newWorkspaceMCPToolActivityRecorder(
	db database.Store,
	publisher databasepubsub.Publisher,
	logger slog.Logger,
	client *codersdk.Client,
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
		historyLimit: int32(boundedHistoryLimit), // #nosec G115 -- clamped to MaxInt32 above.
	}
}

func (r *workspaceMCPToolActivityRecorder) StartToolActivity(
	ctx context.Context,
	_ string,
	toolName string,
	workspaceInput string,
	input string,
	startedAt time.Time,
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
	if err := r.db.InsertWorkspaceToolActivity(activityCtx, database.InsertWorkspaceToolActivityParams{
		ID:          activityID,
		WorkspaceID: workspace.ID,
		Tool:        toolName,
		Command:     input,
		StartedAt:   startedAt,
	}); err != nil {
		return mcp.PersistentActivityHandle{}, xerrors.Errorf("insert workspace MCP tool activity: %w", err)
	}

	r.publishChanged(ctx, workspace.ID, activityID)
	return mcp.PersistentActivityHandle{ID: activityID, WorkspaceID: workspace.ID}, nil
}

func (r *workspaceMCPToolActivityRecorder) FinishToolActivity(
	ctx context.Context,
	handle mcp.PersistentActivityHandle,
	status mcp.PersistentActivityStatus,
	finishedAt time.Time,
) error {
	if r == nil || handle.ID == uuid.Nil || handle.WorkspaceID == uuid.Nil {
		return nil
	}

	activityCtx := dbauthz.AsSystemRestricted(context.WithoutCancel(ctx))
	updated, err := r.db.FinishWorkspaceToolActivity(activityCtx, database.FinishWorkspaceToolActivityParams{
		Status:      string(status),
		FinishedAt:  sql.NullTime{Time: finishedAt, Valid: true},
		ID:          handle.ID,
		WorkspaceID: handle.WorkspaceID,
	})
	if err != nil {
		return xerrors.Errorf("finish workspace MCP tool activity: %w", err)
	}
	if updated == 0 {
		return xerrors.Errorf("finish workspace MCP tool activity: row %s is no longer running", handle.ID)
	}

	r.publishChanged(ctx, handle.WorkspaceID, handle.ID)
	if r.historyLimit > 0 {
		pruned, err := r.db.PruneWorkspaceCommandActivity(activityCtx, database.PruneWorkspaceCommandActivityParams{
			WorkspaceID:  handle.WorkspaceID,
			HistoryLimit: r.historyLimit,
		})
		if err != nil {
			return xerrors.Errorf("prune workspace activity: %w", err)
		}
		if pruned > 0 {
			r.publishResync(ctx, handle.WorkspaceID)
		}
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
