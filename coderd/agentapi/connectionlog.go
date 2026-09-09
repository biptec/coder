package agentapi

import (
	"context"
	"database/sql"
	"sync/atomic"

	"github.com/google/uuid"
	"golang.org/x/xerrors"
	"google.golang.org/protobuf/types/known/emptypb"

	"cdr.dev/slog/v3"
	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/connectionlog"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/db2sdk"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	databasepubsub "github.com/coder/coder/v2/coderd/database/pubsub"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
)

type ConnLogAPI struct {
	AgentID          uuid.UUID
	AgentName        string
	ConnectionLogger *atomic.Pointer[connectionlog.ConnectionLogger]
	Workspace        *CachedWorkspaceFields
	Database         database.Store
	Pubsub           databasepubsub.Publisher
	Log              slog.Logger
}

func (a *ConnLogAPI) ReportConnection(ctx context.Context, req *agentproto.ReportConnectionRequest) (*emptypb.Empty, error) {
	// We use the connection ID to identify which connection log event to mark
	// as closed, when we receive a close action for that ID.
	connectionID, err := uuid.FromBytes(req.GetConnection().GetId())
	if err != nil {
		return nil, xerrors.Errorf("connection id from bytes: %w", err)
	}

	if connectionID == uuid.Nil {
		return nil, xerrors.New("connection ID cannot be nil")
	}
	action, err := db2sdk.ConnectionLogStatusFromAgentProtoConnectionAction(req.GetConnection().GetAction())
	if err != nil {
		return nil, err
	}
	connectionType, err := db2sdk.ConnectionLogConnectionTypeFromAgentProtoConnectionType(req.GetConnection().GetType())
	if err != nil {
		return nil, err
	}

	var code sql.NullInt32
	if action == database.ConnectionStatusDisconnected {
		code = sql.NullInt32{
			Int32: req.GetConnection().GetStatusCode(),
			Valid: true,
		}
	}

	var ws database.WorkspaceIdentity
	if dbws, ok := a.Workspace.AsWorkspaceIdentity(); ok {
		ws = dbws
	}
	if ws.Equal(database.WorkspaceIdentity{}) {
		workspace, err := a.Database.GetWorkspaceByAgentID(ctx, a.AgentID)
		if err != nil {
			return nil, xerrors.Errorf("get workspace by agent id: %w", err)
		}
		ws = database.WorkspaceIdentityFromWorkspace(workspace)
	}

	// Some older clients may incorrectly report "localhost" as the IP address.
	// Related to https://github.com/coder/coder/issues/20194
	logIPRaw := req.GetConnection().GetIp()
	if logIPRaw == "localhost" {
		logIPRaw = "127.0.0.1"
	}
	logIP := database.ParseIP(logIPRaw) // will return null if invalid

	activityTime := req.GetConnection().GetTimestamp().AsTime()
	activityCtx := dbauthz.AsWorkspaceActivity(ctx)
	switch action {
	case database.ConnectionStatusConnected:
		err = a.Database.RecordWorkspaceConnectionStarted(activityCtx, database.RecordWorkspaceConnectionStartedParams{
			ConnectedAt:  sql.NullTime{Time: activityTime, Valid: true},
			WorkspaceID:  ws.ID,
			AgentID:      a.AgentID,
			ConnectionID: connectionID,
			Type:         connectionType,
		})
	case database.ConnectionStatusDisconnected:
		err = a.Database.RecordWorkspaceConnectionFinished(activityCtx, database.RecordWorkspaceConnectionFinishedParams{
			WorkspaceID:    ws.ID,
			AgentID:        a.AgentID,
			Type:           connectionType,
			DisconnectedAt: sql.NullTime{Time: activityTime, Valid: true},
			ConnectionID:   connectionID,
		})
	}
	if err != nil {
		return nil, xerrors.Errorf("record workspace connection activity: %w", err)
	}
	if err := coderdpubsub.PublishWorkspaceActivityEvent(a.Pubsub, ws.ID, coderdpubsub.WorkspaceActivityEvent{
		Type: coderdpubsub.WorkspaceActivityEventConnectionChanged,
	}); err != nil {
		a.Log.Warn(ctx, "publish workspace connection activity", slog.Error(err), slog.F("workspace_id", ws.ID))
	}

	reason := req.GetConnection().GetReason()
	connLogger := *a.ConnectionLogger.Load()
	err = connLogger.Upsert(ctx, database.UpsertConnectionLogParams{
		ID:               uuid.New(),
		Time:             activityTime,
		OrganizationID:   ws.OrganizationID,
		WorkspaceOwnerID: ws.OwnerID,
		WorkspaceID:      ws.ID,
		WorkspaceName:    ws.Name,
		AgentName:        a.AgentName,
		Type:             connectionType,
		Code:             code,
		IP:               logIP,
		ConnectionID: uuid.NullUUID{
			UUID:  connectionID,
			Valid: true,
		},
		DisconnectReason: sql.NullString{
			String: reason,
			Valid:  reason != "",
		},
		// We supply the action:
		// - So the DB can handle duplicate connections or disconnections properly.
		// - To make it clear whether this is a connection or disconnection
		//   prior to it's insertion into the DB (logs)
		ConnectionStatus: action,

		// It's not possible to tell which user connected. Once we have
		// the capability, this may be reported by the agent.
		UserID: uuid.NullUUID{
			Valid: false,
		},
		// N/A
		UserAgent: sql.NullString{},
		// N/A
		SlugOrPort: sql.NullString{},
	})
	if err != nil {
		return nil, xerrors.Errorf("export connection log: %w", err)
	}

	return &emptypb.Empty{}, nil
}
