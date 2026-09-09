package pubsub

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"golang.org/x/xerrors"

	databasepubsub "github.com/coder/coder/v2/coderd/database/pubsub"
)

type WorkspaceActivityEventType string

const (
	WorkspaceActivityEventCommandChanged    WorkspaceActivityEventType = "command_changed"
	WorkspaceActivityEventCommandResync     WorkspaceActivityEventType = "command_resync"
	WorkspaceActivityEventConnectionChanged WorkspaceActivityEventType = "connection_changed"
	WorkspaceActivityEventMCPRequestChanged WorkspaceActivityEventType = "mcp_request_changed"
)

type WorkspaceActivityEvent struct {
	Type      WorkspaceActivityEventType `json:"type"`
	CommandID uuid.UUID                  `json:"command_id,omitempty"`
	RequestID uuid.UUID                  `json:"request_id,omitempty"`
}

func WorkspaceActivityEventChannel(workspaceID uuid.UUID) string {
	return fmt.Sprintf("workspace:activity:%s", workspaceID)
}

func MarshalWorkspaceActivityEvent(event WorkspaceActivityEvent) ([]byte, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, xerrors.Errorf("marshal workspace activity event: %w", err)
	}
	return payload, nil
}

func PublishWorkspaceActivityEvent(publisher databasepubsub.Publisher, workspaceID uuid.UUID, event WorkspaceActivityEvent) error {
	if publisher == nil {
		return nil
	}
	payload, err := MarshalWorkspaceActivityEvent(event)
	if err != nil {
		return err
	}
	if err := publisher.Publish(WorkspaceActivityEventChannel(workspaceID), payload); err != nil {
		return xerrors.Errorf("publish workspace activity event: %w", err)
	}
	return nil
}

func HandleWorkspaceActivityEvent(cb func(ctx context.Context, payload WorkspaceActivityEvent, err error)) func(ctx context.Context, message []byte, err error) {
	return func(ctx context.Context, message []byte, err error) {
		if err != nil {
			cb(ctx, WorkspaceActivityEvent{}, xerrors.Errorf("workspace activity pubsub: %w", err))
			return
		}
		var payload WorkspaceActivityEvent
		if err := json.Unmarshal(message, &payload); err != nil {
			cb(ctx, WorkspaceActivityEvent{}, xerrors.Errorf("unmarshal workspace activity event: %w", err))
			return
		}
		cb(ctx, payload, nil)
	}
}
