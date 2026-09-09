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

func TestWorkspaceCommandActivityHistoryQueries(t *testing.T) {
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
	agentID := uuid.New()
	sessionID := uuid.New()
	base := time.Date(2026, time.September, 9, 6, 0, 0, 0, time.UTC)

	type activitySpec struct {
		id       uuid.UUID
		source   string
		tool     string
		command  string
		argv     []string
		started  time.Time
		duration time.Duration
		exitCode *int32
	}

	exit0 := int32(0)
	exit1 := int32(1)
	specs := []activitySpec{
		{id: uuid.New(), source: "mcp", tool: "exec", argv: []string{"git", "status"}, started: base.Add(5 * time.Second), duration: 100 * time.Millisecond, exitCode: &exit0},
		{id: uuid.New(), source: "ssh", command: "make test needle", started: base.Add(4 * time.Second), duration: 2 * time.Second, exitCode: &exit1},
		{id: uuid.New(), source: "mcp", tool: "bash", command: "sleep 3", started: base.Add(3 * time.Second), duration: 3 * time.Second, exitCode: &exit0},
		{id: uuid.New(), source: "chat", command: "printf chat", started: base.Add(2 * time.Second), duration: 50 * time.Millisecond, exitCode: &exit0},
		{id: uuid.New(), source: "mcp", tool: "process_start", command: "sleep 60", started: base.Add(1 * time.Second)},
	}

	for _, spec := range specs {
		if spec.argv == nil {
			spec.argv = []string{}
		}
		err := db.InsertWorkspaceCommandActivity(t.Context(), database.InsertWorkspaceCommandActivityParams{
			ID:          spec.id,
			WorkspaceID: workspace.ID,
			AgentID:     agentID,
			SessionID:   sessionID,
			Source:      spec.source,
			Tool:        spec.tool,
			Command:     spec.command,
			Argv:        spec.argv,
			WorkDir:     "/workspace",
			StartedAt:   spec.started,
		})
		require.NoError(t, err)
		if spec.exitCode == nil {
			continue
		}
		_, err = db.FinishWorkspaceCommandActivity(t.Context(), database.FinishWorkspaceCommandActivityParams{
			ExitCode:    sql.NullInt32{Int32: *spec.exitCode, Valid: true},
			FinishedAt:  sql.NullTime{Time: spec.started.Add(spec.duration), Valid: true},
			ID:          spec.id,
			WorkspaceID: workspace.ID,
			AgentID:     agentID,
			SessionID:   sessionID,
		})
		require.NoError(t, err)
	}

	baseFilter := database.CountWorkspaceCommandActivityParams{
		WorkspaceID:   workspace.ID,
		DurationMinMs: -1,
		DurationMaxMs: -1,
	}
	count, err := db.CountWorkspaceCommandActivity(t.Context(), baseFilter)
	require.NoError(t, err)
	require.EqualValues(t, 5, count)

	mcpFilter := baseFilter
	mcpFilter.Sources = []string{"mcp"}
	count, err = db.CountWorkspaceCommandActivity(t.Context(), mcpFilter)
	require.NoError(t, err)
	require.EqualValues(t, 3, count)

	searchFilter := baseFilter
	searchFilter.Search = "needle"
	count, err = db.CountWorkspaceCommandActivity(t.Context(), searchFilter)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)

	durationFilter := baseFilter
	durationFilter.DurationMinMs = 1_000
	durationFilter.DurationMaxMs = 2_500
	count, err = db.CountWorkspaceCommandActivity(t.Context(), durationFilter)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)

	page1, err := db.ListWorkspaceCommandActivity(t.Context(), database.ListWorkspaceCommandActivityParams{
		WorkspaceID:   workspace.ID,
		DurationMinMs: -1,
		DurationMaxMs: -1,
		SortBy:        "started",
		SortDirection: "desc",
		PageLimit:     2,
		PageOffset:    0,
	})
	require.NoError(t, err)
	require.Len(t, page1, 2)
	require.Equal(t, specs[0].id, page1[0].ID)
	require.Equal(t, specs[1].id, page1[1].ID)

	page2, err := db.ListWorkspaceCommandActivity(t.Context(), database.ListWorkspaceCommandActivityParams{
		WorkspaceID:   workspace.ID,
		DurationMinMs: -1,
		DurationMaxMs: -1,
		SortBy:        "started",
		SortDirection: "desc",
		PageLimit:     2,
		PageOffset:    2,
	})
	require.NoError(t, err)
	require.Len(t, page2, 2)
	require.Equal(t, specs[2].id, page2[0].ID)
	require.Equal(t, specs[3].id, page2[1].ID)

	deleted, err := db.DeleteWorkspaceCommandActivityByFilter(t.Context(), database.DeleteWorkspaceCommandActivityByFilterParams{
		WorkspaceID:   workspace.ID,
		Sources:       []string{"ssh"},
		DurationMinMs: -1,
		DurationMaxMs: -1,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)

	deleted, err = db.DeleteWorkspaceCommandActivityByIDs(t.Context(), database.DeleteWorkspaceCommandActivityByIDsParams{
		WorkspaceID: workspace.ID,
		IDs:         []uuid.UUID{specs[3].id},
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)

	count, err = db.CountWorkspaceCommandActivity(t.Context(), baseFilter)
	require.NoError(t, err)
	require.EqualValues(t, 3, count)

	// Idle rows describe workspace-level gaps, not gaps between individual
	// commands. Overlapping commands must be merged into one busy interval.
	idleWorkspace := dbgen.Workspace(t, db, database.WorkspaceTable{
		OwnerID:        owner.ID,
		OrganizationID: organization.ID,
		TemplateID:     template.ID,
	})
	idleBase := base.Add(time.Hour)
	insertFinished := func(start, finish time.Time) {
		id := uuid.New()
		err := db.InsertWorkspaceCommandActivity(t.Context(), database.InsertWorkspaceCommandActivityParams{
			ID:          id,
			WorkspaceID: idleWorkspace.ID,
			AgentID:     agentID,
			SessionID:   sessionID,
			Source:      "mcp",
			Tool:        "exec",
			Argv:        []string{"true"},
			WorkDir:     "/workspace",
			StartedAt:   start,
		})
		require.NoError(t, err)
		_, err = db.FinishWorkspaceCommandActivity(t.Context(), database.FinishWorkspaceCommandActivityParams{
			ExitCode:    sql.NullInt32{Int32: 0, Valid: true},
			FinishedAt:  sql.NullTime{Time: finish, Valid: true},
			ID:          id,
			WorkspaceID: idleWorkspace.ID,
			AgentID:     agentID,
			SessionID:   sessionID,
		})
		require.NoError(t, err)
	}
	insertFinished(idleBase, idleBase.Add(10*time.Second))
	insertFinished(idleBase.Add(5*time.Second), idleBase.Add(15*time.Second))
	insertFinished(idleBase.Add(20*time.Second), idleBase.Add(21*time.Second))

	// A tool-only MCP call is visible in the same timeline, but it must not
	// split workspace Idle because Idle describes command execution only.
	toolActivityID := uuid.New()
	err = db.InsertWorkspaceToolActivity(t.Context(), database.InsertWorkspaceToolActivityParams{
		ID:          toolActivityID,
		WorkspaceID: idleWorkspace.ID,
		Tool:        "process_output",
		StartedAt:   idleBase.Add(17 * time.Second),
	})
	require.NoError(t, err)
	updated, err := db.FinishWorkspaceToolActivity(t.Context(), database.FinishWorkspaceToolActivityParams{
		Status:      "succeeded",
		FinishedAt:  sql.NullTime{Time: idleBase.Add(17*time.Second + 100*time.Millisecond), Valid: true},
		ID:          toolActivityID,
		WorkspaceID: idleWorkspace.ID,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, updated)

	toolRows, err := db.ListWorkspaceCommandActivityTimeline(t.Context(), database.ListWorkspaceCommandActivityTimelineParams{
		WorkspaceID:   idleWorkspace.ID,
		Statuses:      []string{"succeeded"},
		Tools:         []string{"process_output"},
		DurationMinMs: -1,
		DurationMaxMs: -1,
		SortBy:        "started",
		SortDirection: "asc",
		PageLimit:     10,
	})
	require.NoError(t, err)
	require.Len(t, toolRows, 1)
	require.Equal(t, toolActivityID, toolRows[0].ID)
	require.Equal(t, "tool", toolRows[0].Kind)
	require.Equal(t, "process_output", toolRows[0].Tool)

	availableTools, err := db.ListWorkspaceCommandActivityTools(t.Context(), idleWorkspace.ID)
	require.NoError(t, err)
	require.Contains(t, availableTools, "process_output")

	idleRows, err := db.ListWorkspaceCommandActivityTimeline(t.Context(), database.ListWorkspaceCommandActivityTimelineParams{
		WorkspaceID:   idleWorkspace.ID,
		Statuses:      []string{"idle"},
		DurationMinMs: -1,
		DurationMaxMs: -1,
		SortBy:        "started",
		SortDirection: "asc",
		PageLimit:     10,
	})
	require.NoError(t, err)
	require.Len(t, idleRows, 2)
	require.True(t, idleBase.Add(15*time.Second).Equal(idleRows[0].StartedAt))
	require.True(t, idleRows[0].FinishedAt.Valid)
	require.True(t, idleBase.Add(20*time.Second).Equal(idleRows[0].FinishedAt.Time))
	require.True(t, idleBase.Add(21*time.Second).Equal(idleRows[1].StartedAt))
	require.False(t, idleRows[1].FinishedAt.Valid, "latest idle interval should remain live")

	timelineCount, err := db.CountWorkspaceCommandActivityTimeline(t.Context(), database.CountWorkspaceCommandActivityTimelineParams{
		WorkspaceID:   idleWorkspace.ID,
		Statuses:      []string{"running", "succeeded", "failed", "interrupted", "idle"},
		DurationMinMs: -1,
		DurationMaxMs: -1,
	})
	require.NoError(t, err)
	require.EqualValues(t, 6, timelineCount, "three commands, one tool call, and two workspace idle intervals")
}
