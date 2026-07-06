package chatd //nolint:testpackage // Uses unexported chatworker helpers.

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/testutil"
)

// TestChatWorker_InspectChat drives a real chatWorker/runnerManager through a
// live task (no HTTP, no fake InspectChat) so that removing setActiveTaskKind
// from runner.go would fail this test, per CRF-22.
func TestChatWorker_InspectChat(t *testing.T) {
	t.Parallel()
	f := newWorkerTestFixture(t)
	chat := f.createRunningChat(t)
	starter := newBlockingTaskStarter(false)
	opts := testOptions(t, f, starter)
	worker := startWorker(t, opts)
	starter.waitCall(t, TaskKindGeneration, chat.ID)

	latest, err := f.db.GetChatByID(testutil.Context(t, testutil.WaitShort), chat.ID)
	require.NoError(t, err)
	require.True(t, latest.RunnerID.Valid)

	snaps := worker.InspectChat(chat.ID)
	require.Len(t, snaps, 1)
	require.Equal(t, latest.RunnerID.UUID, snaps[0].RunnerID)
	require.Equal(t, opts.WorkerID, snaps[0].WorkerID)
	require.NotNil(t, snaps[0].ActiveTaskKind)
	require.Equal(t, TaskKindGeneration, *snaps[0].ActiveTaskKind)
}

// TestRunnerManager_InspectChatStableOrder verifies InspectChat's output
// order is stable across repeated calls, per CRF-23 (map iteration order is
// not stable on its own).
func TestRunnerManager_InspectChatStableOrder(t *testing.T) {
	t.Parallel()

	chatID := uuid.New()
	byRunner := make(map[uuid.UUID]*runnerRecord)
	for i := 0; i < 5; i++ {
		runnerID := uuid.New()
		byRunner[runnerID] = &runnerRecord{
			key:      runnerKey{ChatID: chatID, RunnerID: runnerID},
			workerID: uuid.New(),
		}
	}
	m := &runnerManager{
		runnersByChat: map[uuid.UUID]map[uuid.UUID]*runnerRecord{
			chatID: byRunner,
		},
	}

	first := m.InspectChat(chatID)
	require.Len(t, first, len(byRunner))
	for i := 0; i < 20; i++ {
		require.Equal(t, first, m.InspectChat(chatID), "InspectChat order must be stable across repeated calls")
	}
}
