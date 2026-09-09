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

	idFilter := baseFilter
	idFilter.IDSearch = specs[2].id.String()[:10]
	count, err = db.CountWorkspaceCommandActivity(t.Context(), idFilter)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)

	exitFilter := baseFilter
	exitFilter.ExitCode = sql.NullInt32{Int32: 1, Valid: true}
	count, err = db.CountWorkspaceCommandActivity(t.Context(), exitFilter)
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

	// Tool-only MCP activity is stored in the same history and keeps its
	// sanitized request input in the existing command text column.
	toolWorkspace := dbgen.Workspace(t, db, database.WorkspaceTable{
		OwnerID:        owner.ID,
		OrganizationID: organization.ID,
		TemplateID:     template.ID,
	})
	toolActivityID := uuid.New()
	toolInput := `{"workspace":"owner/tool-workspace","process_id":"process-123"}`
	err = db.InsertWorkspaceToolActivity(t.Context(), database.InsertWorkspaceToolActivityParams{
		ID:          toolActivityID,
		WorkspaceID: toolWorkspace.ID,
		Tool:        "process_output",
		Command:     toolInput,
		StartedAt:   base.Add(time.Hour),
	})
	require.NoError(t, err)
	updated, err := db.FinishWorkspaceToolActivity(t.Context(), database.FinishWorkspaceToolActivityParams{
		Status:      "succeeded",
		FinishedAt:  sql.NullTime{Time: base.Add(time.Hour + 100*time.Millisecond), Valid: true},
		ID:          toolActivityID,
		WorkspaceID: toolWorkspace.ID,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, updated)

	toolRows, err := db.ListWorkspaceCommandActivity(t.Context(), database.ListWorkspaceCommandActivityParams{
		WorkspaceID:   toolWorkspace.ID,
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
	require.Equal(t, toolInput, toolRows[0].Command)

	availableTools, err := db.ListWorkspaceCommandActivityTools(t.Context(), toolWorkspace.ID)
	require.NoError(t, err)
	require.Contains(t, availableTools, "process_output")

	// A command written by a pre-attribution agent can contain a trusted MCP
	// tool marker while its raw source is still agentproc. Source=MCP must treat
	// that compatibility row exactly like a native MCP row in LIST/COUNT/DELETE.
	legacyMCPWorkspace := dbgen.Workspace(t, db, database.WorkspaceTable{
		OwnerID:        owner.ID,
		OrganizationID: organization.ID,
		TemplateID:     template.ID,
	})
	legacyMCPID := uuid.New()
	err = db.InsertWorkspaceCommandActivity(t.Context(), database.InsertWorkspaceCommandActivityParams{
		ID:          legacyMCPID,
		WorkspaceID: legacyMCPWorkspace.ID,
		AgentID:     uuid.New(),
		SessionID:   uuid.New(),
		Source:      "agentproc",
		Tool:        "exec",
		Command:     "echo legacy mcp",
		Argv:        []string{},
		StartedAt:   base.Add(2 * time.Hour),
	})
	require.NoError(t, err)

	legacyMCPFilter := database.ListWorkspaceCommandActivityParams{
		WorkspaceID:   legacyMCPWorkspace.ID,
		Sources:       []string{"mcp"},
		DurationMinMs: -1,
		DurationMaxMs: -1,
		SortBy:        "started",
		SortDirection: "desc",
		PageLimit:     10,
	}
	legacyRows, err := db.ListWorkspaceCommandActivity(t.Context(), legacyMCPFilter)
	require.NoError(t, err)
	require.Len(t, legacyRows, 1)
	require.Equal(t, legacyMCPID, legacyRows[0].ID)

	legacyCount, err := db.CountWorkspaceCommandActivity(t.Context(), database.CountWorkspaceCommandActivityParams{
		WorkspaceID:   legacyMCPWorkspace.ID,
		Sources:       []string{"mcp"},
		DurationMinMs: -1,
		DurationMaxMs: -1,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, legacyCount)

	legacyDeleted, err := db.DeleteWorkspaceCommandActivityByFilter(t.Context(), database.DeleteWorkspaceCommandActivityByFilterParams{
		WorkspaceID:   legacyMCPWorkspace.ID,
		Sources:       []string{"mcp"},
		DurationMinMs: -1,
		DurationMaxMs: -1,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, legacyDeleted)

}
