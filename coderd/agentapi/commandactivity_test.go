package agentapi_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentproto "github.com/coder/coder/v2/agent/proto"
	"github.com/coder/coder/v2/coderd/agentapi"
	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/coder/v2/codersdk/toolsdk"
	"github.com/coder/coder/v2/testutil"
)

func TestCommandActivity(t *testing.T) {
	t.Parallel()

	workspaceID := uuid.New()
	agentID := uuid.New()
	sessionID := uuid.New()
	activityID := uuid.New()
	activityTime := time.Date(2026, time.September, 8, 12, 34, 56, 0, time.UTC)

	t.Run("StartAndFinish", func(t *testing.T) {
		t.Parallel()

		mDB := dbmock.NewMockStore(gomock.NewController(t))
		api := &agentapi.CommandActivityAPI{
			AgentID:      agentID,
			WorkspaceID:  workspaceID,
			Database:     mDB,
			HistoryLimit: 25,
			Log:          testutil.Logger(t),
		}

		mDB.EXPECT().InsertWorkspaceCommandActivity(gomock.Any(), database.InsertWorkspaceCommandActivityParams{
			ID:          activityID,
			WorkspaceID: workspaceID,
			AgentID:     agentID,
			SessionID:   sessionID,
			Source:      "mcp",
			Tool:        "exec",
			Command:     "echo hello",
			Argv:        []string{},
			WorkDir:     "/workspace",
			StartedAt:   activityTime,
		}).Return(nil)

		_, err := api.ReportCommandActivity(context.Background(), &agentproto.ReportCommandActivityRequest{
			Activity: &agentproto.CommandActivity{
				Id:        activityID[:],
				SessionId: sessionID[:],
				Action:    agentproto.CommandActivity_STARTED,
				Source:    agentproto.CommandActivity_AGENTPROC,
				Tool:      "exec",
				Command:   "echo hello",
				WorkDir:   "/workspace",
				Timestamp: timestamppb.New(activityTime),
			},
		})
		require.NoError(t, err)

		exitCode := int32(42)
		mDB.EXPECT().FinishWorkspaceCommandActivity(gomock.Any(), database.FinishWorkspaceCommandActivityParams{
			ExitCode:    sql.NullInt32{Int32: exitCode, Valid: true},
			FinishedAt:  sql.NullTime{Time: activityTime, Valid: true},
			ID:          activityID,
			WorkspaceID: workspaceID,
			AgentID:     agentID,
			SessionID:   sessionID,
		}).Return(int64(1), nil)
		mDB.EXPECT().PruneWorkspaceCommandActivity(gomock.Any(), database.PruneWorkspaceCommandActivityParams{
			WorkspaceID:  workspaceID,
			HistoryLimit: 25,
		}).Return(int64(0), nil)

		_, err = api.ReportCommandActivity(context.Background(), &agentproto.ReportCommandActivityRequest{
			Activity: &agentproto.CommandActivity{
				Id:        activityID[:],
				SessionId: sessionID[:],
				Action:    agentproto.CommandActivity_FINISHED,
				Timestamp: timestamppb.New(activityTime),
				ExitCode:  &exitCode,
			},
		})
		require.NoError(t, err)
	})

	t.Run("LegacyAgentProcWithoutMCPToolStaysInternal", func(t *testing.T) {
		t.Parallel()

		legacyActivityID := uuid.New()
		mDB := dbmock.NewMockStore(gomock.NewController(t))
		api := &agentapi.CommandActivityAPI{
			AgentID:     agentID,
			WorkspaceID: workspaceID,
			Database:    mDB,
			Log:         testutil.Logger(t),
		}

		mDB.EXPECT().ListWorkspaceMCPRequestActivityCandidates(gomock.Any(), database.ListWorkspaceMCPRequestActivityCandidatesParams{
			WorkspaceID: workspaceID,
			Tools: []string{
				"exec", "bash", "process_start",
				toolsdk.ToolNameWorkspaceExec,
				toolsdk.ToolNameWorkspaceBash,
				toolsdk.ToolNameWorkspaceProcessStart,
				toolsdk.ToolNameWorkspaceProcessStartV2,
			},
			CorrelationHash: toolsdk.CommandActivityCorrelation("legacy command", []string{}),
			ActivityTime:    activityTime,
		}).Return(nil, nil)
		mDB.EXPECT().InsertWorkspaceCommandActivity(gomock.Any(), database.InsertWorkspaceCommandActivityParams{
			ID:          legacyActivityID,
			WorkspaceID: workspaceID,
			AgentID:     agentID,
			SessionID:   sessionID,
			Source:      "agentproc",
			Command:     "legacy command",
			Argv:        []string{},
			WorkDir:     "/workspace",
			StartedAt:   activityTime,
		}).Return(nil)

		_, err := api.ReportCommandActivity(context.Background(), &agentproto.ReportCommandActivityRequest{
			Activity: &agentproto.CommandActivity{
				Id:        legacyActivityID[:],
				SessionId: sessionID[:],
				Action:    agentproto.CommandActivity_STARTED,
				Source:    agentproto.CommandActivity_AGENTPROC,
				Command:   "legacy command",
				WorkDir:   "/workspace",
				Timestamp: timestamppb.New(activityTime),
			},
		})
		require.NoError(t, err)
	})

	t.Run("LegacyAgentProcCorrelatesMCPBash", func(t *testing.T) {
		t.Parallel()

		legacyActivityID := uuid.New()
		mDB := dbmock.NewMockStore(gomock.NewController(t))
		api := &agentapi.CommandActivityAPI{
			AgentID:     agentID,
			WorkspaceID: workspaceID,
			Database:    mDB,
			Log:         testutil.Logger(t),
		}

		mDB.EXPECT().ListWorkspaceMCPRequestActivityCandidates(gomock.Any(), database.ListWorkspaceMCPRequestActivityCandidatesParams{
			WorkspaceID: workspaceID,
			Tools: []string{
				"exec", "bash", "process_start",
				toolsdk.ToolNameWorkspaceExec,
				toolsdk.ToolNameWorkspaceBash,
				toolsdk.ToolNameWorkspaceProcessStart,
				toolsdk.ToolNameWorkspaceProcessStartV2,
			},
			CorrelationHash: toolsdk.CommandActivityCorrelation("echo from old agent", []string{}),
			ActivityTime:    activityTime,
		}).Return([]database.WorkspaceMcpRequestActivity{
			{
				ID:          uuid.New(),
				WorkspaceID: workspaceID,
				ReplicaID:   uuid.New(),
				Tool:        "bash",
				Input:       `{"workspace":"developer/network-core-route","command":"echo from old agent"}`,
				Status:      "running",
				StartedAt:   activityTime.Add(-100 * time.Millisecond),
			},
		}, nil)
		mDB.EXPECT().InsertWorkspaceCommandActivity(gomock.Any(), database.InsertWorkspaceCommandActivityParams{
			ID:          legacyActivityID,
			WorkspaceID: workspaceID,
			AgentID:     agentID,
			SessionID:   sessionID,
			Source:      "mcp",
			Tool:        "bash",
			Command:     "echo from old agent",
			Argv:        []string{},
			WorkDir:     "/workspace",
			StartedAt:   activityTime,
		}).Return(nil)

		_, err := api.ReportCommandActivity(context.Background(), &agentproto.ReportCommandActivityRequest{
			Activity: &agentproto.CommandActivity{
				Id:        legacyActivityID[:],
				SessionId: sessionID[:],
				Action:    agentproto.CommandActivity_STARTED,
				Source:    agentproto.CommandActivity_AGENTPROC,
				Command:   "echo from old agent",
				WorkDir:   "/workspace",
				Timestamp: timestamppb.New(activityTime),
			},
		})
		require.NoError(t, err)
	})

	t.Run("UnlimitedHistoryDoesNotPrune", func(t *testing.T) {
		t.Parallel()

		mDB := dbmock.NewMockStore(gomock.NewController(t))
		api := &agentapi.CommandActivityAPI{
			AgentID:      agentID,
			WorkspaceID:  workspaceID,
			Database:     mDB,
			HistoryLimit: 0,
			Log:          testutil.Logger(t),
		}

		mDB.EXPECT().InsertWorkspaceCommandActivity(gomock.Any(), database.InsertWorkspaceCommandActivityParams{
			ID:          activityID,
			WorkspaceID: workspaceID,
			AgentID:     agentID,
			SessionID:   sessionID,
			Source:      "mcp",
			Tool:        "exec",
			Command:     "echo unlimited",
			Argv:        []string{},
			WorkDir:     "/workspace",
			StartedAt:   activityTime,
		}).Return(nil)

		_, err := api.ReportCommandActivity(context.Background(), &agentproto.ReportCommandActivityRequest{
			Activity: &agentproto.CommandActivity{
				Id:        activityID[:],
				SessionId: sessionID[:],
				Action:    agentproto.CommandActivity_STARTED,
				Source:    agentproto.CommandActivity_MCP,
				Tool:      "exec",
				Command:   "echo unlimited",
				WorkDir:   "/workspace",
				Timestamp: timestamppb.New(activityTime),
			},
		})
		require.NoError(t, err)

		exitCode := int32(0)
		mDB.EXPECT().FinishWorkspaceCommandActivity(gomock.Any(), database.FinishWorkspaceCommandActivityParams{
			ExitCode:    sql.NullInt32{Int32: exitCode, Valid: true},
			FinishedAt:  sql.NullTime{Time: activityTime, Valid: true},
			ID:          activityID,
			WorkspaceID: workspaceID,
			AgentID:     agentID,
			SessionID:   sessionID,
		}).Return(int64(1), nil)

		_, err = api.ReportCommandActivity(context.Background(), &agentproto.ReportCommandActivityRequest{
			Activity: &agentproto.CommandActivity{
				Id:        activityID[:],
				SessionId: sessionID[:],
				Action:    agentproto.CommandActivity_FINISHED,
				Timestamp: timestamppb.New(activityTime),
				ExitCode:  &exitCode,
			},
		})
		require.NoError(t, err)
	})

	t.Run("NewAgentSessionInterruptsStaleState", func(t *testing.T) {
		t.Parallel()

		mDB := dbmock.NewMockStore(gomock.NewController(t))
		api := &agentapi.CommandActivityAPI{
			AgentID:      agentID,
			WorkspaceID:  workspaceID,
			Database:     mDB,
			HistoryLimit: 25,
			Log:          testutil.Logger(t),
		}

		mDB.EXPECT().InterruptWorkspaceCommandActivityByAgentSession(gomock.Any(), database.InterruptWorkspaceCommandActivityByAgentSessionParams{
			FinishedAt:  sql.NullTime{Time: activityTime, Valid: true},
			WorkspaceID: workspaceID,
			AgentID:     agentID,
			SessionID:   sessionID,
		}).Return(int64(2), nil)
		mDB.EXPECT().ResetWorkspaceActiveConnectionsByAgentID(gomock.Any(), database.ResetWorkspaceActiveConnectionsByAgentIDParams{
			DisconnectedAt: sql.NullTime{Time: activityTime, Valid: true},
			WorkspaceID:    workspaceID,
			AgentID:        agentID,
		}).Return(nil)
		mDB.EXPECT().PruneWorkspaceCommandActivity(gomock.Any(), database.PruneWorkspaceCommandActivityParams{
			WorkspaceID:  workspaceID,
			HistoryLimit: 25,
		}).Return(int64(0), nil)

		_, err := api.ReportCommandActivity(context.Background(), &agentproto.ReportCommandActivityRequest{
			Activity: &agentproto.CommandActivity{
				SessionId: sessionID[:],
				Action:    agentproto.CommandActivity_SESSION_STARTED,
				Timestamp: timestamppb.New(activityTime),
			},
		})
		require.NoError(t, err)
	})

	t.Run("StartedRequiresCommand", func(t *testing.T) {
		t.Parallel()

		mDB := dbmock.NewMockStore(gomock.NewController(t))
		api := &agentapi.CommandActivityAPI{
			AgentID:      agentID,
			WorkspaceID:  workspaceID,
			Database:     mDB,
			HistoryLimit: 25,
			Log:          testutil.Logger(t),
		}

		_, err := api.ReportCommandActivity(context.Background(), &agentproto.ReportCommandActivityRequest{
			Activity: &agentproto.CommandActivity{
				Id:        activityID[:],
				SessionId: sessionID[:],
				Action:    agentproto.CommandActivity_STARTED,
				Source:    agentproto.CommandActivity_SSH,
				Timestamp: timestamppb.New(activityTime),
			},
		})
		require.ErrorContains(t, err, "command or argv is required")
	})
}
