package database_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
)

func TestWorkspaceMCPRequestActivityQueries(t *testing.T) {
	t.Parallel()

	db, _ := dbtestutil.NewDB(t)
	owner := dbgen.User(t, db, database.User{})
	organization, err := db.GetDefaultOrganization(t.Context())
	require.NoError(t, err)
	template := dbgen.Template(t, db, database.Template{
		OrganizationID: organization.ID,
		CreatedBy:      owner.ID,
	})
	workspace := dbgen.Workspace(t, db, database.WorkspaceTable{
		OwnerID:        owner.ID,
		OrganizationID: organization.ID,
		TemplateID:     template.ID,
	})

	replicaID := uuid.New()
	base := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	insert := func(id uuid.UUID, tool, input string, startedAt time.Time) {
		t.Helper()
		require.NoError(t, db.InsertWorkspaceMCPRequestActivity(t.Context(), database.InsertWorkspaceMCPRequestActivityParams{
			ID:          id,
			WorkspaceID: workspace.ID,
			ReplicaID:   replicaID,
			Tool:        tool,
			Input:       input,
			StartedAt:   startedAt,
		}))
	}
	finish := func(id uuid.UUID, status string, finishedAt time.Time) {
		t.Helper()
		updated, err := db.FinishWorkspaceMCPRequestActivity(t.Context(), database.FinishWorkspaceMCPRequestActivityParams{
			Status:      status,
			FinishedAt:  sql.NullTime{Time: finishedAt, Valid: true},
			ID:          id,
			WorkspaceID: workspace.ID,
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, updated)
	}

	previousID := uuid.New()
	middleID := uuid.New()
	runningID := uuid.New()
	insert(previousID, "read_file", `{"workspace":"developer/example"}`, base)
	finish(previousID, "succeeded", base.Add(time.Second))
	insert(middleID, "process_output", `{"workspace":"developer/example","process_id":"p1"}`, base.Add(2*time.Second))
	finish(middleID, "succeeded", base.Add(3*time.Second))
	insert(runningID, "bash", `{"workspace":"developer/example","command":"sleep 30"}`, base.Add(4*time.Second))

	current, err := db.ListWorkspaceMCPRequestActivityCurrent(t.Context(), workspace.ID)
	require.NoError(t, err)
	require.Len(t, current, 2)
	require.Equal(t, middleID, current[0].ID, "current state keeps the latest completed request as the Idle anchor")
	require.Equal(t, runningID, current[1].ID, "all active requests must be returned")
	require.False(t, current[1].FinishedAt.Valid)

	rangeRows, err := db.ListWorkspaceMCPRequestActivityForRange(t.Context(), database.ListWorkspaceMCPRequestActivityForRangeParams{
		WorkspaceID: workspace.ID,
		RangeStart:  base.Add(1500 * time.Millisecond),
		RangeEnd:    base.Add(3500 * time.Millisecond),
	})
	require.NoError(t, err)
	require.Len(t, rangeRows, 2)
	require.Equal(t, previousID, rangeRows[0].ID, "range queries include one completed predecessor to anchor the leading Idle gap")
	require.Equal(t, middleID, rangeRows[1].ID)

	candidates, err := db.ListWorkspaceMCPRequestActivityCandidates(t.Context(), database.ListWorkspaceMCPRequestActivityCandidatesParams{
		WorkspaceID:  workspace.ID,
		Tools:        []string{"bash"},
		ActivityTime: base.Add(4 * time.Second),
	})
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, runningID, candidates[0].ID)

	pruned, err := db.PruneWorkspaceMCPRequestActivity(t.Context(), database.PruneWorkspaceMCPRequestActivityParams{
		WorkspaceID:  workspace.ID,
		HistoryLimit: 1,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, pruned)

	_, err = db.GetWorkspaceMCPRequestActivityByID(t.Context(), database.GetWorkspaceMCPRequestActivityByIDParams{
		WorkspaceID: workspace.ID,
		ID:          previousID,
	})
	require.ErrorIs(t, err, sql.ErrNoRows)
	latest, err := db.GetWorkspaceMCPRequestActivityByID(t.Context(), database.GetWorkspaceMCPRequestActivityByIDParams{
		WorkspaceID: workspace.ID,
		ID:          middleID,
	})
	require.NoError(t, err)
	require.Equal(t, "succeeded", latest.Status)
	running, err := db.GetWorkspaceMCPRequestActivityByID(t.Context(), database.GetWorkspaceMCPRequestActivityByIDParams{
		WorkspaceID: workspace.ID,
		ID:          runningID,
	})
	require.NoError(t, err)
	require.Equal(t, "running", running.Status, "retention pruning must never delete an active request")
}
