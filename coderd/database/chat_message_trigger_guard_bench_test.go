//go:build bench_chat_search

package database_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/dbgen"
	"github.com/coder/coder/v2/coderd/database/dbtestutil"
	"github.com/coder/coder/v2/coderd/x/chatd/chatprompt"
)

// Trigger-guard variants for set_chat_message_revision_before. All three
// share the 000519 body; B and C differ only in the extra early-return guard
// that ignores changes confined to search_tsv. The AFTER statement triggers
// stay at their migrated (000541) versions for every run.
const triggerGuardCommonHead = `
CREATE OR REPLACE FUNCTION set_chat_message_revision_before()
RETURNS trigger AS $$
DECLARE
    chat_snapshot_version bigint;
%s
BEGIN
    IF TG_OP = 'INSERT' AND NEW.revision IS NOT NULL THEN
        RAISE EXCEPTION 'chat_messages.revision must be assigned by trigger';
    END IF;

    IF TG_OP = 'UPDATE' THEN
        IF OLD.chat_id IS DISTINCT FROM NEW.chat_id THEN
            RAISE EXCEPTION 'chat_messages.chat_id is immutable';
        END IF;

        IF OLD.revision IS DISTINCT FROM NEW.revision THEN
            RAISE EXCEPTION 'chat_messages.revision must be assigned by trigger';
        END IF;

        IF OLD IS NOT DISTINCT FROM NEW THEN
            RETURN NEW;
        END IF;
%s
    END IF;

    SELECT snapshot_version INTO chat_snapshot_version
    FROM chats WHERE id = NEW.chat_id;

    IF chat_snapshot_version IS NULL THEN
        RAISE EXCEPTION 'chat %% does not exist', NEW.chat_id;
    END IF;

    NEW.revision = chat_snapshot_version;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
`

type triggerGuardVariant struct {
	Name string
	SQL  string
}

func triggerGuardVariants() []triggerGuardVariant {
	return []triggerGuardVariant{
		{
			// Original 000519 body: no search_tsv guard at all.
			Name: "A_no_guard",
			SQL:  fmt.Sprintf(triggerGuardCommonHead, "", ""),
		},
		{
			// Current 000541 body: jsonb-minus comparison.
			Name: "B_jsonb_minus",
			SQL: fmt.Sprintf(triggerGuardCommonHead, "", `
        IF to_jsonb(OLD) - 'search_tsv' = to_jsonb(NEW) - 'search_tsv' THEN
            RETURN NEW;
        END IF;`),
		},
		{
			// Composite-copy comparison: overwrite search_tsv on a row copy
			// and compare whole rows.
			Name: "C_composite_copy",
			SQL: fmt.Sprintf(triggerGuardCommonHead, `
    cmp chat_messages;`, `
        cmp := NEW;
        cmp.search_tsv := OLD.search_tsv;
        IF OLD IS NOT DISTINCT FROM cmp THEN
            RETURN NEW;
        END IF;`),
		},
	}
}

// BenchmarkChatSearchTriggerGuard measures the row-trigger cost of the three
// guard variants on the two UPDATE paths that matter:
//   - backfill: the real sweep UPDATE (search_tsv only) in 10k batches until
//     drained,
//   - edit: content updates on eligible rows (full trigger path).
//
// Run:
//
//	go test -tags bench_chat_search -run=^$ -bench=BenchmarkChatSearchTriggerGuard \
//	  -benchtime=1x -timeout=60m -v ./coderd/database
func BenchmarkChatSearchTriggerGuard(b *testing.B) {
	ctx := b.Context()
	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(b)

	seedStart := time.Now()
	seeded := seedTriggerGuardCorpus(ctx, b, db)
	b.Logf("seed: messages=%d duration=%s", seeded, time.Since(seedStart))

	_, err := sqlDB.ExecContext(ctx, `ANALYZE chats; ANALYZE chat_messages;`)
	require.NoError(b, err)

	var eligible int64
	err = sqlDB.QueryRowContext(ctx, `
SELECT count(*) FROM chat_messages
WHERE search_tsv IS NULL
  AND deleted = false
  AND visibility IN ('user', 'both')
  AND role IN ('user', 'assistant');
`).Scan(&eligible)
	require.NoError(b, err)
	b.Logf("backfill-eligible rows: %d (%.0f%% of seeded)", eligible, 100*float64(eligible)/float64(seeded))

	// Sanity: chats must be idle so the AFTER trigger's chats UPDATE clause
	// stays cheap and comparable across variants.
	var busyChats int64
	err = sqlDB.QueryRowContext(ctx, `
SELECT count(*) FROM chats
WHERE generation_attempt <> 0 OR history_version IS DISTINCT FROM snapshot_version;
`).Scan(&busyChats)
	require.NoError(b, err)
	require.Zero(b, busyChats, "chats must be idle before benchmarking")

	prepareTriggerGuardEditRows(ctx, b, sqlDB, 5000)

	variants := triggerGuardVariants()
	byName := map[string]triggerGuardVariant{}
	for _, v := range variants {
		byName[v.Name] = v
	}

	type pathStats struct {
		wall      []time.Duration
		latencies []time.Duration
	}
	backfill := map[string]*pathStats{}
	edits := map[string]*pathStats{}
	for _, v := range variants {
		backfill[v.Name] = &pathStats{}
		edits[v.Name] = &pathStats{}
	}

	// Warmup: one full pass per variant, unmeasured.
	for _, v := range variants {
		installTriggerGuardVariant(ctx, b, sqlDB, v)
		runTriggerGuardBackfillDrain(ctx, b, sqlDB, eligible)
		resetTriggerGuardTsv(ctx, b, sqlDB)
		runTriggerGuardEditBatches(ctx, b, sqlDB)
		restoreTriggerGuardEditRows(ctx, b, sqlDB)
	}

	// Measured: A,B,C then C,B,A to cancel drift; results averaged.
	order := []string{"A_no_guard", "B_jsonb_minus", "C_composite_copy", "C_composite_copy", "B_jsonb_minus", "A_no_guard"}
	for run, name := range order {
		v := byName[name]
		installTriggerGuardVariant(ctx, b, sqlDB, v)

		wall, lats := runTriggerGuardBackfillDrain(ctx, b, sqlDB, eligible)
		backfill[name].wall = append(backfill[name].wall, wall)
		backfill[name].latencies = append(backfill[name].latencies, lats...)
		resetTriggerGuardTsv(ctx, b, sqlDB)

		editWall, editLats := runTriggerGuardEditBatches(ctx, b, sqlDB)
		edits[name].wall = append(edits[name].wall, editWall)
		edits[name].latencies = append(edits[name].latencies, editLats...)
		restoreTriggerGuardEditRows(ctx, b, sqlDB)

		b.Logf("run %d variant=%s backfill_wall=%s edit_wall=%s", run+1, name, wall, editWall)
	}

	report := func(label string, stats map[string]*pathStats) {
		for _, v := range variants {
			s := stats[v.Name]
			var total time.Duration
			for _, w := range s.wall {
				total += w
			}
			avg := total / time.Duration(len(s.wall))
			p50, _, p99, maxLat := chatMessageSearchDurationPercentiles(s.latencies)
			b.Logf("%s variant=%s avg_wall=%s batch_p50=%s batch_p99=%s batch_max=%s (batches=%d)",
				label, v.Name, avg, p50, p99, maxLat, len(s.latencies))
		}
	}
	report("backfill", backfill)
	report("edit", edits)
}

// AFTER statement trigger variants for update_chat_history_after_message_update.
// The BEFORE trigger stays at its migrated (composite-copy) version. X is the
// migrated 000541 body; Y replaces the jsonb-minus row comparison with a
// plpgsql helper doing a composite-copy comparison.
const afterTriggerVariantX = `
CREATE OR REPLACE FUNCTION update_chat_history_after_message_update()
RETURNS trigger AS $$
BEGIN
    UPDATE chats c
    SET history_version = c.snapshot_version,
        generation_attempt = 0
    FROM (
        SELECT DISTINCT n.chat_id
        FROM chat_message_history_new_rows n
        JOIN chat_message_history_old_rows o ON o.id = n.id
        WHERE (to_jsonb(o) - 'search_tsv') IS DISTINCT FROM (to_jsonb(n) - 'search_tsv')
    ) AS affected
    WHERE c.id = affected.chat_id
      AND (
          c.history_version IS DISTINCT FROM c.snapshot_version
          OR c.generation_attempt <> 0
      );
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
`

const afterTriggerVariantY = `
CREATE OR REPLACE FUNCTION chat_messages_differ_ignoring_search_tsv(o chat_messages, n chat_messages)
RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    cmp chat_messages;
BEGIN
    cmp := n;
    cmp.search_tsv := o.search_tsv;
    RETURN o IS DISTINCT FROM cmp;
END;
$$;

CREATE OR REPLACE FUNCTION update_chat_history_after_message_update()
RETURNS trigger AS $$
BEGIN
    UPDATE chats c
    SET history_version = c.snapshot_version,
        generation_attempt = 0
    FROM (
        SELECT DISTINCT n.chat_id
        FROM chat_message_history_new_rows n
        JOIN chat_message_history_old_rows o ON o.id = n.id
        WHERE chat_messages_differ_ignoring_search_tsv(o, n)
    ) AS affected
    WHERE c.id = affected.chat_id
      AND (
          c.history_version IS DISTINCT FROM c.snapshot_version
          OR c.generation_attempt <> 0
      );
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
`

// BenchmarkChatSearchAfterTriggerGuard compares the jsonb-minus (X) and
// plpgsql helper (Y) row comparisons inside the AFTER statement trigger on
// both UPDATE paths. The BEFORE row trigger is left at its migrated
// composite-copy version throughout.
//
// Run:
//
//	go test -tags bench_chat_search -run=^$ -bench=BenchmarkChatSearchAfterTriggerGuard \
//	  -benchtime=1x -timeout=60m -v ./coderd/database
func BenchmarkChatSearchAfterTriggerGuard(b *testing.B) {
	ctx := b.Context()
	db, _, sqlDB := dbtestutil.NewDBWithSQLDB(b)

	seedStart := time.Now()
	seeded := seedTriggerGuardCorpus(ctx, b, db)
	b.Logf("seed: messages=%d duration=%s", seeded, time.Since(seedStart))

	_, err := sqlDB.ExecContext(ctx, `ANALYZE chats; ANALYZE chat_messages;`)
	require.NoError(b, err)

	var eligible int64
	err = sqlDB.QueryRowContext(ctx, `
SELECT count(*) FROM chat_messages
WHERE search_tsv IS NULL
  AND deleted = false
  AND visibility IN ('user', 'both')
  AND role IN ('user', 'assistant');
`).Scan(&eligible)
	require.NoError(b, err)
	b.Logf("backfill-eligible rows: %d (%.0f%% of seeded)", eligible, 100*float64(eligible)/float64(seeded))

	prepareTriggerGuardEditRows(ctx, b, sqlDB, 5000)

	variants := []triggerGuardVariant{
		{Name: "X_jsonb_minus", SQL: afterTriggerVariantX},
		{Name: "Y_plpgsql_helper", SQL: afterTriggerVariantY},
	}
	byName := map[string]triggerGuardVariant{}
	for _, v := range variants {
		byName[v.Name] = v
	}

	type pathStats struct {
		wall      []time.Duration
		latencies []time.Duration
	}
	backfill := map[string]*pathStats{}
	edits := map[string]*pathStats{}
	for _, v := range variants {
		backfill[v.Name] = &pathStats{}
		edits[v.Name] = &pathStats{}
	}

	// Warmup: one full pass per variant, unmeasured.
	for _, v := range variants {
		installTriggerGuardVariant(ctx, b, sqlDB, v)
		runTriggerGuardBackfillDrain(ctx, b, sqlDB, eligible)
		resetTriggerGuardTsv(ctx, b, sqlDB)
		runTriggerGuardEditBatches(ctx, b, sqlDB)
		restoreTriggerGuardEditRows(ctx, b, sqlDB)
	}

	// Measured: X,Y then Y,X to cancel drift; results averaged.
	order := []string{"X_jsonb_minus", "Y_plpgsql_helper", "Y_plpgsql_helper", "X_jsonb_minus"}
	for run, name := range order {
		v := byName[name]
		installTriggerGuardVariant(ctx, b, sqlDB, v)

		wall, lats := runTriggerGuardBackfillDrain(ctx, b, sqlDB, eligible)
		backfill[name].wall = append(backfill[name].wall, wall)
		backfill[name].latencies = append(backfill[name].latencies, lats...)
		resetTriggerGuardTsv(ctx, b, sqlDB)

		editWall, editLats := runTriggerGuardEditBatches(ctx, b, sqlDB)
		edits[name].wall = append(edits[name].wall, editWall)
		edits[name].latencies = append(edits[name].latencies, editLats...)
		restoreTriggerGuardEditRows(ctx, b, sqlDB)

		b.Logf("run %d variant=%s backfill_wall=%s edit_wall=%s", run+1, name, wall, editWall)
	}

	report := func(label string, stats map[string]*pathStats) {
		for _, v := range variants {
			s := stats[v.Name]
			var total time.Duration
			for _, w := range s.wall {
				total += w
			}
			avg := total / time.Duration(len(s.wall))
			p50, _, p99, maxLat := chatMessageSearchDurationPercentiles(s.latencies)
			b.Logf("%s variant=%s avg_wall=%s batch_p50=%s batch_p99=%s batch_max=%s (batches=%d)",
				label, v.Name, avg, p50, p99, maxLat, len(s.latencies))
		}
	}
	report("backfill", backfill)
	report("edit", edits)
}

// seedTriggerGuardCorpus seeds ~100k messages across 20 users with the
// content-size long tail from chatSearchTextBytes but a majority-eligible
// role mix: 70% assistant text, 20% user text, 10% tool results.
func seedTriggerGuardCorpus(ctx context.Context, t testing.TB, db database.Store) int {
	t.Helper()

	faker := gofakeit.New(1)
	organization := dbgen.Organization(t, db, database.Organization{})
	modelConfig := dbgen.ChatModelConfig(t, db, database.ChatModelConfig{})

	const users = 20
	const chatsPerUser = 50
	const avgMessages = 100

	chatCounter := 0
	seeded := 0
	for range users {
		owner := dbgen.User(t, db, database.User{})
		apiKey, _ := dbgen.APIKey(t, db, database.APIKey{UserID: owner.ID})
		for range chatsPerUser {
			chat := dbgen.Chat(t, db, database.Chat{
				OrganizationID:    organization.ID,
				OwnerID:           owner.ID,
				LastModelConfigID: modelConfig.ID,
				Title:             fmt.Sprintf("trigger guard bench chat %d", chatCounter),
			})
			messageCount := jitteredMessageCount(faker, avgMessages)
			params := triggerGuardBatchParams(faker, chat, owner.ID, apiKey.ID, modelConfig.ID, chatCounter, messageCount)
			_, err := db.InsertChatMessages(ctx, params)
			require.NoError(t, err)
			seeded += messageCount
			chatCounter++
		}
	}
	return seeded
}

func triggerGuardBatchParams(faker *gofakeit.Faker, chat database.Chat, ownerID uuid.UUID, apiKeyID string, modelConfigID uuid.UUID, chatIndex, messagesPerChat int) database.InsertChatMessagesParams {
	params := database.InsertChatMessagesParams{
		ChatID:              chat.ID,
		CreatedBy:           make([]uuid.UUID, messagesPerChat),
		APIKeyID:            make([]string, messagesPerChat),
		ModelConfigID:       make([]uuid.UUID, messagesPerChat),
		Role:                make([]database.ChatMessageRole, messagesPerChat),
		Content:             make([]string, messagesPerChat),
		ContentVersion:      make([]int16, messagesPerChat),
		Visibility:          make([]database.ChatMessageVisibility, messagesPerChat),
		InputTokens:         make([]int64, messagesPerChat),
		OutputTokens:        make([]int64, messagesPerChat),
		TotalTokens:         make([]int64, messagesPerChat),
		ReasoningTokens:     make([]int64, messagesPerChat),
		CacheCreationTokens: make([]int64, messagesPerChat),
		CacheReadTokens:     make([]int64, messagesPerChat),
		ContextLimit:        make([]int64, messagesPerChat),
		Compressed:          make([]bool, messagesPerChat),
		TotalCostMicros:     make([]int64, messagesPerChat),
		RuntimeMs:           make([]int64, messagesPerChat),
	}

	for messageIndex := range messagesPerChat {
		absoluteIndex := chatIndex*messagesPerChat + messageIndex
		role := database.ChatMessageRoleAssistant
		content := chatSearchTextContentJSON(chatSearchSeedText(faker, absoluteIndex))
		createdBy := uuid.Nil
		keyID := ""
		switch bucket := absoluteIndex % 10; {
		case bucket < 7:
			// assistant text: defaults above.
		case bucket < 9:
			role = database.ChatMessageRoleUser
			createdBy = ownerID
			keyID = apiKeyID
		default:
			role = database.ChatMessageRoleTool
			content = `[]`
		}

		params.CreatedBy[messageIndex] = createdBy
		params.APIKeyID[messageIndex] = keyID
		params.ModelConfigID[messageIndex] = modelConfigID
		params.Role[messageIndex] = role
		params.Content[messageIndex] = content
		params.ContentVersion[messageIndex] = chatprompt.CurrentContentVersion
		params.Visibility[messageIndex] = database.ChatMessageVisibilityBoth
	}

	return params
}

func installTriggerGuardVariant(ctx context.Context, t testing.TB, sqlDB *sql.DB, v triggerGuardVariant) {
	t.Helper()
	_, err := sqlDB.ExecContext(ctx, v.SQL)
	require.NoError(t, err, "install variant %s", v.Name)
}

// runTriggerGuardBackfillDrain runs the real 10k-batch sweep UPDATE until
// drained, returning total wall time and per-batch latencies.
func runTriggerGuardBackfillDrain(ctx context.Context, t testing.TB, sqlDB *sql.DB, eligible int64) (time.Duration, []time.Duration) {
	t.Helper()

	sweepSQL := chatMessageSearchSweepSQL(10_000)
	var latencies []time.Duration
	var drained int64
	start := time.Now()
	for {
		batchStart := time.Now()
		res, err := sqlDB.ExecContext(ctx, sweepSQL)
		require.NoError(t, err)
		rows, err := res.RowsAffected()
		require.NoError(t, err)
		if rows == 0 {
			break
		}
		drained += rows
		latencies = append(latencies, time.Since(batchStart))
	}
	total := time.Since(start)
	require.Equal(t, eligible, drained, "drain must touch every eligible row")
	return total, latencies
}

// resetTriggerGuardTsv restores the post-migration state between backfill
// runs: search_tsv back to NULL, then a plain (not FULL) VACUUM.
func resetTriggerGuardTsv(ctx context.Context, t testing.TB, sqlDB *sql.DB) {
	t.Helper()

	_, err := sqlDB.ExecContext(ctx, `UPDATE chat_messages SET search_tsv = NULL WHERE search_tsv IS NOT NULL;`)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, `VACUUM chat_messages;`)
	require.NoError(t, err)
}

// prepareTriggerGuardEditRows snapshots N random eligible rows so edit runs
// can mutate content and restore it afterwards.
func prepareTriggerGuardEditRows(ctx context.Context, t testing.TB, sqlDB *sql.DB, n int) {
	t.Helper()

	_, err := sqlDB.ExecContext(ctx, fmt.Sprintf(`
CREATE UNLOGGED TABLE bench_edit_rows AS
SELECT cm.id, cm.content, (row_number() OVER (ORDER BY cm.id)) %% 5 AS batch
FROM chat_messages cm
WHERE cm.deleted = false
  AND cm.visibility IN ('user', 'both')
  AND cm.role IN ('user', 'assistant')
ORDER BY random()
LIMIT %d;
CREATE INDEX ON bench_edit_rows (batch, id);
ANALYZE bench_edit_rows;
`, n))
	require.NoError(t, err)
}

// runTriggerGuardEditBatches updates content on the sampled rows in 5 batches
// of ~1k rows each, exercising the full trigger path (content changes, so no
// guard short-circuits).
func runTriggerGuardEditBatches(ctx context.Context, t testing.TB, sqlDB *sql.DB) (time.Duration, []time.Duration) {
	t.Helper()

	var latencies []time.Duration
	start := time.Now()
	for batch := range 5 {
		batchStart := time.Now()
		res, err := sqlDB.ExecContext(ctx, `
UPDATE chat_messages cm
SET content = cm.content || '[{"type":"text","text":"bench edit marker"}]'::jsonb
FROM bench_edit_rows b
WHERE cm.id = b.id AND b.batch = $1;
`, batch)
		require.NoError(t, err)
		rows, err := res.RowsAffected()
		require.NoError(t, err)
		require.Positive(t, rows)
		latencies = append(latencies, time.Since(batchStart))
	}
	return time.Since(start), latencies
}

// restoreTriggerGuardEditRows puts original content back so every run edits
// identical rows. The restore itself fires the triggers but is unmeasured.
func restoreTriggerGuardEditRows(ctx context.Context, t testing.TB, sqlDB *sql.DB) {
	t.Helper()

	_, err := sqlDB.ExecContext(ctx, `
UPDATE chat_messages cm
SET content = b.content
FROM bench_edit_rows b
WHERE cm.id = b.id;
`)
	require.NoError(t, err)
	_, err = sqlDB.ExecContext(ctx, `VACUUM chat_messages;`)
	require.NoError(t, err)
}
