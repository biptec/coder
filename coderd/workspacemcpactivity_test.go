package coderd

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbmock"
	"github.com/coder/coder/v2/testutil"
)

func TestWorkspaceMCPConnectionTracker(t *testing.T) {
	t.Parallel()

	workspaceID := uuid.New()
	agentID := uuid.New()
	mDB := dbmock.NewMockStore(gomock.NewController(t))

	mDB.EXPECT().RecordWorkspaceConnectionActivityStarted(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, arg database.RecordWorkspaceConnectionActivityStartedParams) error {
			require.Equal(t, workspaceID, arg.WorkspaceID)
			require.Equal(t, agentID, arg.AgentID)
			require.Equal(t, database.ConnectionTypeMcp, arg.Type)
			require.True(t, arg.ConnectedAt.Valid)
			return nil
		},
	)
	mDB.EXPECT().RecordWorkspaceConnectionActivityFinished(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ any, arg database.RecordWorkspaceConnectionActivityFinishedParams) error {
			require.Equal(t, workspaceID, arg.WorkspaceID)
			require.Equal(t, agentID, arg.AgentID)
			require.Equal(t, database.ConnectionTypeMcp, arg.Type)
			require.True(t, arg.DisconnectedAt.Valid)
			return nil
		},
	)

	tracker := newWorkspaceMCPConnectionTracker(mDB, nil, testutil.Logger(t))
	finish := tracker.Start(t.Context(), workspaceID, agentID)
	require.EqualValues(t, 1, tracker.Active(workspaceID))
	require.Zero(t, tracker.Active(uuid.New()))

	finish()
	require.Zero(t, tracker.Active(workspaceID))

	// Releasing an already released logical connection must be idempotent.
	finish()
	require.Zero(t, tracker.Active(workspaceID))
}
