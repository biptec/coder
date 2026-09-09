package coderd

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/google/uuid"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	databasepubsub "github.com/coder/coder/v2/coderd/database/pubsub"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
)

type workspaceMCPConnectionKey struct {
	workspaceID uuid.UUID
	agentID     uuid.UUID
}

type workspaceMCPConnectionTracker struct {
	mu     sync.Mutex
	active map[workspaceMCPConnectionKey]int32
	db     database.Store
	pubsub databasepubsub.Publisher
	logger slog.Logger
}

func newWorkspaceMCPConnectionTracker(db database.Store, publisher databasepubsub.Publisher, logger slog.Logger) *workspaceMCPConnectionTracker {
	return &workspaceMCPConnectionTracker{
		active: make(map[workspaceMCPConnectionKey]int32),
		db:     db,
		pubsub: publisher,
		logger: logger.Named("workspace-mcp-activity"),
	}
}

func (t *workspaceMCPConnectionTracker) Start(ctx context.Context, workspaceID, agentID uuid.UUID) func() {
	key := workspaceMCPConnectionKey{workspaceID: workspaceID, agentID: agentID}
	connectedAt := time.Now()

	t.mu.Lock()
	t.active[key]++
	t.mu.Unlock()

	// MCP tool handlers use a clean context that intentionally strips arbitrary
	// context values. Authorization has already been enforced by the HTTP handler,
	// so persist the internal connection summary with a restricted system context.
	activityCtx := dbauthz.AsSystemRestricted(ctx)
	if err := t.db.RecordWorkspaceConnectionActivityStarted(activityCtx, database.RecordWorkspaceConnectionActivityStartedParams{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		Type:        database.ConnectionTypeMcp,
		ConnectedAt: sql.NullTime{Time: connectedAt, Valid: true},
	}); err != nil {
		t.logger.Warn(ctx, "record MCP connection start", slog.Error(err), slog.F("workspace_id", workspaceID), slog.F("agent_id", agentID))
	}
	t.publishConnectionChanged(ctx, workspaceID)

	var once sync.Once
	return func() {
		once.Do(func() {
			disconnectedAt := time.Now()
			t.mu.Lock()
			if current := t.active[key]; current <= 1 {
				delete(t.active, key)
			} else {
				t.active[key] = current - 1
			}
			t.mu.Unlock()

			finishCtx := dbauthz.AsSystemRestricted(context.Background())
			if err := t.db.RecordWorkspaceConnectionActivityFinished(finishCtx, database.RecordWorkspaceConnectionActivityFinishedParams{
				WorkspaceID:    workspaceID,
				AgentID:        agentID,
				Type:           database.ConnectionTypeMcp,
				DisconnectedAt: sql.NullTime{Time: disconnectedAt, Valid: true},
			}); err != nil {
				t.logger.Warn(context.Background(), "record MCP connection finish", slog.Error(err), slog.F("workspace_id", workspaceID), slog.F("agent_id", agentID))
			}
			t.publishConnectionChanged(context.Background(), workspaceID)
		})
	}
}

func (t *workspaceMCPConnectionTracker) publishConnectionChanged(ctx context.Context, workspaceID uuid.UUID) {
	if err := coderdpubsub.PublishWorkspaceActivityEvent(t.pubsub, workspaceID, coderdpubsub.WorkspaceActivityEvent{
		Type: coderdpubsub.WorkspaceActivityEventConnectionChanged,
	}); err != nil {
		t.logger.Warn(ctx, "publish MCP connection activity", slog.Error(err), slog.F("workspace_id", workspaceID))
	}
}

func (t *workspaceMCPConnectionTracker) Active(workspaceID uuid.UUID) int32 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	var total int32
	for key, count := range t.active {
		if key.workspaceID == workspaceID {
			total += count
		}
	}
	return total
}
