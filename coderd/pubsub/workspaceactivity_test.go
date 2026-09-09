package pubsub

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceActivityEventRoundTrip(t *testing.T) {
	t.Parallel()

	commandID := uuid.New()
	payload, err := MarshalWorkspaceActivityEvent(WorkspaceActivityEvent{
		Type:      WorkspaceActivityEventCommandChanged,
		CommandID: commandID,
	})
	require.NoError(t, err)

	var got WorkspaceActivityEvent
	var gotErr error
	handler := HandleWorkspaceActivityEvent(func(_ context.Context, event WorkspaceActivityEvent, err error) {
		got = event
		gotErr = err
	})
	handler(t.Context(), payload, nil)

	require.NoError(t, gotErr)
	require.Equal(t, WorkspaceActivityEventCommandChanged, got.Type)
	require.Equal(t, commandID, got.CommandID)
}

func TestWorkspaceActivityEventMalformedPayload(t *testing.T) {
	t.Parallel()

	var gotErr error
	handler := HandleWorkspaceActivityEvent(func(_ context.Context, _ WorkspaceActivityEvent, err error) {
		gotErr = err
	})
	handler(t.Context(), []byte("not-json"), nil)

	require.Error(t, gotErr)
}
