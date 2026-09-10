package agentapi

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"golang.org/x/xerrors"
	"google.golang.org/protobuf/types/known/emptypb"

	"cdr.dev/slog/v3"
	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbauthz"
	databasepubsub "github.com/coder/coder/v2/coderd/database/pubsub"
	coderdpubsub "github.com/coder/coder/v2/coderd/pubsub"
	"github.com/coder/coder/v2/codersdk/toolsdk"
)

type CommandActivityAPI struct {
	AgentID      uuid.UUID
	WorkspaceID  uuid.UUID
	Database     database.Store
	Pubsub       databasepubsub.Publisher
	HistoryLimit int32
	Log          slog.Logger
}

func (a *CommandActivityAPI) ReportCommandActivity(ctx context.Context, req *agentproto.ReportCommandActivityRequest) (*emptypb.Empty, error) {
	activity := req.GetActivity()
	if activity == nil {
		return nil, xerrors.New("command activity is required")
	}

	sessionID, err := uuid.FromBytes(activity.GetSessionId())
	if err != nil {
		return nil, xerrors.Errorf("command activity session id: %w", err)
	}
	if sessionID == uuid.Nil {
		return nil, xerrors.New("command activity session id cannot be nil")
	}
	if activity.GetTimestamp() == nil {
		return nil, xerrors.New("command activity timestamp is required")
	}
	if err := activity.GetTimestamp().CheckValid(); err != nil {
		return nil, xerrors.Errorf("command activity timestamp: %w", err)
	}
	activityTime := activity.GetTimestamp().AsTime()
	activityCtx := dbauthz.AsWorkspaceActivity(ctx)

	switch activity.GetAction() {
	case agentproto.CommandActivity_SESSION_STARTED:
		_, err = a.Database.InterruptWorkspaceCommandActivityByAgentSession(activityCtx, database.InterruptWorkspaceCommandActivityByAgentSessionParams{
			FinishedAt:  sql.NullTime{Time: activityTime, Valid: true},
			WorkspaceID: a.WorkspaceID,
			AgentID:     a.AgentID,
			SessionID:   sessionID,
		})
		if err != nil {
			return nil, xerrors.Errorf("interrupt command activity from previous agent session: %w", err)
		}
		if err := a.Database.ResetWorkspaceActiveConnectionsByAgentID(activityCtx, database.ResetWorkspaceActiveConnectionsByAgentIDParams{
			DisconnectedAt: sql.NullTime{Time: activityTime, Valid: true},
			WorkspaceID:    a.WorkspaceID,
			AgentID:        a.AgentID,
		}); err != nil {
			return nil, xerrors.Errorf("reset active connections from previous agent session: %w", err)
		}
		if err := a.prune(activityCtx); err != nil {
			return nil, err
		}
		a.publishActivityEvent(ctx, coderdpubsub.WorkspaceActivityEvent{Type: coderdpubsub.WorkspaceActivityEventCommandResync})

	case agentproto.CommandActivity_STARTED:
		activityID, err := commandActivityID(activity)
		if err != nil {
			return nil, err
		}
		tool := activity.GetTool()
		sourceValue := activity.GetSource()
		if sourceValue == agentproto.CommandActivity_AGENTPROC && tool == "" {
			inferredTool, ok, inferErr := a.inferLegacyMCPCommandTool(activityCtx, activityTime, activity.GetCommand(), activity.GetArgv())
			if inferErr != nil {
				a.Log.Debug(ctx, "infer legacy MCP command activity", slog.Error(inferErr), slog.F("workspace_id", a.WorkspaceID), slog.F("agent_id", a.AgentID))
			} else if ok {
				tool = inferredTool
				sourceValue = agentproto.CommandActivity_MCP
			}
		}
		source, err := commandActivitySource(sourceValue, tool)
		if err != nil {
			return nil, err
		}
		if activity.GetCommand() == "" && len(activity.GetArgv()) == 0 {
			return nil, xerrors.New("command activity command or argv is required")
		}
		if err := a.Database.InsertWorkspaceCommandActivity(activityCtx, database.InsertWorkspaceCommandActivityParams{
			ID:          activityID,
			WorkspaceID: a.WorkspaceID,
			AgentID:     a.AgentID,
			SessionID:   sessionID,
			Source:      source,
			Tool:        tool,
			Command:     activity.GetCommand(),
			// Keep argv non-nil so pq.Array serializes command-string activity as
			// an empty PostgreSQL array instead of NULL. The column is NOT NULL.
			Argv:      append([]string{}, activity.GetArgv()...),
			WorkDir:   activity.GetWorkDir(),
			StartedAt: activityTime,
		}); err != nil {
			return nil, xerrors.Errorf("insert workspace command activity: %w", err)
		}
		a.publishActivityEvent(ctx, coderdpubsub.WorkspaceActivityEvent{
			Type:      coderdpubsub.WorkspaceActivityEventCommandChanged,
			CommandID: activityID,
		})

	case agentproto.CommandActivity_FINISHED:
		activityID, err := commandActivityID(activity)
		if err != nil {
			return nil, err
		}
		if activity.ExitCode == nil {
			return nil, xerrors.New("finished command activity exit code is required")
		}
		updated, err := a.Database.FinishWorkspaceCommandActivity(activityCtx, database.FinishWorkspaceCommandActivityParams{
			ExitCode:    sql.NullInt32{Int32: activity.GetExitCode(), Valid: true},
			FinishedAt:  sql.NullTime{Time: activityTime, Valid: true},
			ID:          activityID,
			WorkspaceID: a.WorkspaceID,
			AgentID:     a.AgentID,
			SessionID:   sessionID,
		})
		if err != nil {
			return nil, xerrors.Errorf("finish workspace command activity: %w", err)
		}
		if updated > 0 {
			a.publishActivityEvent(ctx, coderdpubsub.WorkspaceActivityEvent{
				Type:      coderdpubsub.WorkspaceActivityEventCommandChanged,
				CommandID: activityID,
			})
		}
		if err := a.prune(activityCtx); err != nil {
			return nil, err
		}
		if a.HistoryLimit > 0 {
			a.publishActivityEvent(ctx, coderdpubsub.WorkspaceActivityEvent{Type: coderdpubsub.WorkspaceActivityEventCommandResync})
		}

	default:
		return nil, xerrors.Errorf("unsupported command activity action %q", activity.GetAction().String())
	}

	return &emptypb.Empty{}, nil
}

func (a *CommandActivityAPI) publishActivityEvent(ctx context.Context, event coderdpubsub.WorkspaceActivityEvent) {
	if err := coderdpubsub.PublishWorkspaceActivityEvent(a.Pubsub, a.WorkspaceID, event); err != nil {
		a.Log.Warn(ctx, "publish workspace activity event", slog.Error(err), slog.F("workspace_id", a.WorkspaceID), slog.F("event_type", event.Type))
	}
}

func (a *CommandActivityAPI) prune(ctx context.Context) error {
	if a.HistoryLimit <= 0 {
		return nil
	}
	if _, err := a.Database.PruneWorkspaceCommandActivity(ctx, database.PruneWorkspaceCommandActivityParams{
		WorkspaceID:  a.WorkspaceID,
		HistoryLimit: a.HistoryLimit,
	}); err != nil {
		return xerrors.Errorf("prune workspace command activity: %w", err)
	}
	return nil
}

func commandActivityID(activity *agentproto.CommandActivity) (uuid.UUID, error) {
	id, err := uuid.FromBytes(activity.GetId())
	if err != nil {
		return uuid.Nil, xerrors.Errorf("command activity id: %w", err)
	}
	if id == uuid.Nil {
		return uuid.Nil, xerrors.New("command activity id cannot be nil")
	}
	return id, nil
}

func (a *CommandActivityAPI) inferLegacyMCPCommandTool(
	ctx context.Context,
	activityTime time.Time,
	command string,
	argv []string,
) (string, bool, error) {
	correlationHash := toolsdk.CommandActivityCorrelation(command, argv)
	candidates, err := a.Database.ListWorkspaceMCPRequestActivityCandidates(ctx, database.ListWorkspaceMCPRequestActivityCandidatesParams{
		WorkspaceID: a.WorkspaceID,
		Tools: []string{
			"exec", "bash", "process_start",
			toolsdk.ToolNameWorkspaceExec,
			toolsdk.ToolNameWorkspaceBash,
			toolsdk.ToolNameWorkspaceProcessStart,
			toolsdk.ToolNameWorkspaceProcessStartV2,
		},
		CorrelationHash: correlationHash,
		ActivityTime:    activityTime,
	})
	if err != nil {
		return "", false, err
	}

	inferred := ""
	for _, candidate := range candidates {
		candidateTool := canonicalMCPCommandTool(candidate.Tool)
		if candidateTool == "" {
			continue
		}
		if inferred != "" && inferred != candidateTool {
			// The same command shape can legitimately be launched through bash or
			// process_start at nearly the same time. Do not guess across tools.
			return "", false, nil
		}
		inferred = candidateTool
	}
	return inferred, inferred != "", nil
}

func canonicalMCPCommandTool(tool string) string {
	switch tool {
	case "exec", toolsdk.ToolNameWorkspaceExec:
		return "exec"
	case "bash", toolsdk.ToolNameWorkspaceBash:
		return "bash"
	case "process_start", toolsdk.ToolNameWorkspaceProcessStart, toolsdk.ToolNameWorkspaceProcessStartV2:
		return "process_start"
	default:
		return ""
	}
}

func commandActivitySource(source agentproto.CommandActivity_Source, tool string) (string, error) {
	switch source {
	case agentproto.CommandActivity_AGENTPROC:
		// Agents older than the source-attribution change report MCP command tools
		// as AGENTPROC. Normalize the trusted tool marker on ingest so rolling
		// upgrades never expose the internal compatibility source to users.
		switch tool {
		case "exec", "bash", "process_start",
			toolsdk.ToolNameWorkspaceExec,
			toolsdk.ToolNameWorkspaceBash,
			toolsdk.ToolNameWorkspaceProcessStart,
			toolsdk.ToolNameWorkspaceProcessStartV2:
			return "mcp", nil
		default:
			return "agentproc", nil
		}
	case agentproto.CommandActivity_SSH:
		return "ssh", nil
	case agentproto.CommandActivity_MCP:
		return "mcp", nil
	case agentproto.CommandActivity_RECONNECTING_PTY:
		return "reconnecting_pty", nil
	case agentproto.CommandActivity_VSCODE:
		return "vscode", nil
	case agentproto.CommandActivity_JETBRAINS:
		return "jetbrains", nil
	case agentproto.CommandActivity_CHAT:
		return "chat", nil
	default:
		return "", xerrors.Errorf("unsupported command activity source %q", source.String())
	}
}
