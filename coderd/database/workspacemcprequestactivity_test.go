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

func TestWorkspaceIdleActivityQueries(t *testing.T) {
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

	base := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	replicaA := uuid.New()
	replicaB := uuid.New()
	insertFinished := func(replicaID uuid.UUID, start, finish time.Duration) {
		t.Helper()
		id := uuid.New()
		require.NoError(t, db.InsertWorkspaceMCPRequestActivity(t.Context(), database.InsertWorkspaceMCPRequestActivityParams{
			ID:          id,
			WorkspaceID: workspace.ID,
			ReplicaID:   replicaID,
			Tool:        "exec",
			StartedAt:   base.Add(start),
		}))
		updated, err := db.FinishWorkspaceMCPRequestActivity(t.Context(), database.FinishWorkspaceMCPRequestActivityParams{
			Status:      "succeeded",
			FinishedAt:  sql.NullTime{Time: base.Add(finish), Valid: true},
			ID:          id,
			WorkspaceID: workspace.ID,
		})
		require.NoError(t, err)
		require.EqualValues(t, 1, updated)
	}

	// Two clients overlap in each of the first two busy islands. Idle is based
	// on the workspace-wide union, not per-client gaps.
	insertFinished(replicaA, 0, 2*time.Second)
	insertFinished(replicaB, time.Second, 3*time.Second)
	insertFinished(replicaA, 5*time.Second, 6*time.Second)
	insertFinished(replicaB, 5500*time.Millisecond, 7*time.Second)
	insertFinished(replicaA, 10*time.Second, 11*time.Second)

	countParams := database.CountWorkspaceIdleActivityParams{
		WorkspaceID:   workspace.ID,
		DurationMinMs: -1,
		DurationMaxMs: -1,
	}
	count, err := db.CountWorkspaceIdleActivity(t.Context(), countParams)
	require.NoError(t, err)
	require.EqualValues(t, 3, count, "two historical gaps plus the current Idle interval")

	page1, err := db.ListWorkspaceIdleActivity(t.Context(), database.ListWorkspaceIdleActivityParams{
		WorkspaceID:   workspace.ID,
		DurationMinMs: -1,
		DurationMaxMs: -1,
		SortDirection: "desc",
		PageLimit:     2,
	})
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.True(t, page1[0].IsCurrent)
	require.Equal(t, base.Add(11*time.Second), page1[0].StartedAt)
	require.False(t, page1[1].IsCurrent)
	require.Equal(t, base.Add(7*time.Second), page1[1].StartedAt)
	require.Equal(t, base.Add(10*time.Second), page1[1].FinishedAt)

	page2, err := db.ListWorkspaceIdleActivity(t.Context(), database.ListWorkspaceIdleActivityParams{
		WorkspaceID:   workspace.ID,
		DurationMinMs: -1,
		DurationMaxMs: -1,
		SortDirection: "desc",
		PageLimit:     2,
		PageOffset:    2,
	})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Equal(t, base.Add(3*time.Second), page2[0].StartedAt)
	require.Equal(t, base.Add(5*time.Second), page2[0].FinishedAt)

	maxTwoSeconds := int64(2_000)
	countParams.DurationMaxMs = maxTwoSeconds
	count, err = db.CountWorkspaceIdleActivity(t.Context(), countParams)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)

	// A new unfinished request ends the current Idle at its start and suppresses
	// any open-ended Idle while the workspace has active MCP work.
	runningID := uuid.New()
	require.NoError(t, db.InsertWorkspaceMCPRequestActivity(t.Context(), database.InsertWorkspaceMCPRequestActivityParams{
		ID:          runningID,
		WorkspaceID: workspace.ID,
		ReplicaID:   replicaB,
		Tool:        "process_output",
		StartedAt:   base.Add(12 * time.Second),
	}))
	countParams.DurationMaxMs = -1
	rows, err := db.ListWorkspaceIdleActivity(t.Context(), database.ListWorkspaceIdleActivityParams{
		WorkspaceID:   workspace.ID,
		DurationMinMs: -1,
		DurationMaxMs: -1,
		SortDirection: "desc",
		PageLimit:     10,
	})
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.False(t, rows[0].IsCurrent)
	require.Equal(t, base.Add(11*time.Second), rows[0].StartedAt)
	require.Equal(t, base.Add(12*time.Second), rows[0].FinishedAt)
}

func TestInterruptStaleWorkspaceMCPRequestActivityUsesRequestHeartbeat(t *testing.T) {
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

	now := time.Now().UTC()
	currentReplicaID := uuid.New()
	orphanReplicaID := uuid.New()
	freshReplicaID := uuid.New()
	currentLiveID := uuid.New()
	currentOrphanID := uuid.New()
	orphanID := uuid.New()
	freshID := uuid.New()
	for _, item := range []struct {
		id        uuid.UUID
		replicaID uuid.UUID
		startedAt time.Time
	}{
		{currentLiveID, currentReplicaID, now.Add(-time.Hour)},
		{currentOrphanID, currentReplicaID, now.Add(-time.Hour)},
		{orphanID, orphanReplicaID, now.Add(-time.Hour)},
		{freshID, freshReplicaID, now.Add(-5 * time.Second)},
	} {
		require.NoError(t, db.InsertWorkspaceMCPRequestActivity(t.Context(), database.InsertWorkspaceMCPRequestActivityParams{
			ID:          item.id,
			WorkspaceID: workspace.ID,
			ReplicaID:   item.replicaID,
			Tool:        "exec",
			StartedAt:   item.startedAt,
		}))
	}

	heartbeatRows, err := db.HeartbeatWorkspaceMCPRequestActivity(t.Context(), database.HeartbeatWorkspaceMCPRequestActivityParams{
		HeartbeatAt: now,
		ID:          currentLiveID,
		WorkspaceID: workspace.ID,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, heartbeatRows)

	updated, err := db.InterruptStaleWorkspaceMCPRequestActivity(t.Context(), database.InterruptStaleWorkspaceMCPRequestActivityParams{
		WorkspaceID:      workspace.ID,
		CurrentReplicaID: currentReplicaID,
		StaleBefore:      now.Add(-30 * time.Second),
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, updated)

	currentLive, err := db.GetWorkspaceMCPRequestActivityByID(t.Context(), database.GetWorkspaceMCPRequestActivityByIDParams{WorkspaceID: workspace.ID, ID: currentLiveID})
	require.NoError(t, err)
	require.Equal(t, "running", currentLive.Status, "a long request with a fresh request heartbeat remains live")
	currentOrphan, err := db.GetWorkspaceMCPRequestActivityByID(t.Context(), database.GetWorkspaceMCPRequestActivityByIDParams{WorkspaceID: workspace.ID, ID: currentOrphanID})
	require.NoError(t, err)
	require.Equal(t, "interrupted", currentOrphan.Status, "the current replica can recover a row whose request heartbeat stopped")
	require.True(t, currentOrphan.FinishedAt.Valid)
	require.WithinDuration(t, now.Add(-time.Hour), currentOrphan.FinishedAt.Time, time.Millisecond, "recovery must end busy time at the last known request heartbeat, not cleanup time")
	orphan, err := db.GetWorkspaceMCPRequestActivityByID(t.Context(), database.GetWorkspaceMCPRequestActivityByIDParams{WorkspaceID: workspace.ID, ID: orphanID})
	require.NoError(t, err)
	require.Equal(t, "interrupted", orphan.Status)
	require.True(t, orphan.FinishedAt.Valid)
	require.WithinDuration(t, now.Add(-time.Hour), orphan.FinishedAt.Time, time.Millisecond)
	fresh, err := db.GetWorkspaceMCPRequestActivityByID(t.Context(), database.GetWorkspaceMCPRequestActivityByIDParams{WorkspaceID: workspace.ID, ID: freshID})
	require.NoError(t, err)
	require.Equal(t, "running", fresh.Status, "a just-started request gets a grace period while replica registration catches up")
}
