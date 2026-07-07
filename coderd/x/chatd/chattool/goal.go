package chattool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"charm.land/fantasy"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/coder/coder/v2/coderd/database"
	"github.com/coder/coder/v2/coderd/database/db2sdk/chatgoal"
	"github.com/coder/coder/v2/codersdk"
)

const (
	GetGoalToolName      = "get_goal"
	CompleteGoalToolName = "complete_goal"
	BlockGoalToolName    = "block_goal"
)

// GoalToolOptions configures the goal tools.
type GoalToolOptions struct {
	ChatID        uuid.UUID
	RootChatID    uuid.UUID
	IsRootChat    bool
	OnGoalUpdated func(context.Context, database.Chat, database.ChatGoal)
	// Fence, when set, must still describe the chat when complete_goal
	// commits. It prevents a stale generation (interrupted or taken over
	// by another worker) from completing the durable goal after its tool
	// result would be rejected by the generation fence.
	Fence *GoalToolFence
}

// GoalToolFence pins the goal mutation to the generation turn that
// offered the tool.
type GoalToolFence struct {
	WorkerID       uuid.UUID
	RunnerID       uuid.UUID
	HistoryVersion int64
}

var errGoalFenceMismatch = xerrors.New("goal tool fence mismatch")

// verifyGoalToolFence locks the chat row and checks that the turn that
// offered complete_goal still owns the chat.
func verifyGoalToolFence(ctx context.Context, tx database.Store, chatID uuid.UUID, fence *GoalToolFence) error {
	if fence == nil {
		return nil
	}
	chat, err := tx.GetChatByIDForUpdate(ctx, chatID)
	if err != nil {
		return err
	}
	if !chat.WorkerID.Valid || chat.WorkerID.UUID != fence.WorkerID ||
		!chat.RunnerID.Valid || chat.RunnerID.UUID != fence.RunnerID ||
		chat.Status != database.ChatStatusRunning ||
		chat.HistoryVersion != fence.HistoryVersion {
		return errGoalFenceMismatch
	}
	return nil
}

type getGoalArgs struct{}

type completeGoalArgs struct {
	GoalID  string `json:"goal_id" description:"The expected current goal ID as a UUIDv4 string. The tool fails if the current goal changed."`
	Summary string `json:"summary" description:"A concise non-empty summary of how the goal was completed."`
}

type blockGoalArgs struct {
	GoalID string `json:"goal_id" description:"The expected current goal ID as a UUIDv4 string. The tool fails if the current goal changed."`
	Reason string `json:"reason" description:"A concise non-empty explanation of what blocks progress and what is needed from the user."`
}

type goalResult struct {
	Goal *codersdk.ChatGoal `json:"goal"`
}

type completeGoalResult struct {
	Goal      *codersdk.ChatGoal `json:"goal"`
	Completed bool               `json:"completed"`
	Summary   string             `json:"summary"`
}

type blockGoalResult struct {
	Goal    *codersdk.ChatGoal `json:"goal"`
	Blocked bool               `json:"blocked"`
	Reason  string             `json:"reason"`
}

// parseGoalIDArg parses the goal_id tool argument shared by the goal
// mutation tools. ok is false when the argument is missing or malformed.
func parseGoalIDArg(raw string) (uuid.UUID, bool) {
	goalIDStr := strings.TrimSpace(raw)
	if goalIDStr == "" {
		return uuid.Nil, false
	}
	goalID, err := uuid.Parse(goalIDStr)
	if err != nil {
		return uuid.Nil, false
	}
	return goalID, true
}

// mutateCurrentActiveGoal runs the transaction shared by the goal
// mutation tools: it locks the chat row for the fence check, verifies
// the current goal matches goalID and is active, enforces the combined
// text payload limit for the extra textLen bytes, applies update, and
// reloads the chat for post-commit callbacks.
func mutateCurrentActiveGoal(
	ctx context.Context,
	db database.Store,
	options GoalToolOptions,
	goalID uuid.UUID,
	textLen int,
	update func(tx database.Store) (database.ChatGoal, error),
) (database.ChatGoal, database.Chat, error) {
	var updated database.ChatGoal
	var chat database.Chat
	err := db.InTx(func(tx database.Store) error {
		// Lock the chat row first (matching the API mutation paths) so
		// the fence check and goal update are atomic with respect to
		// interrupts and worker takeovers.
		if err := verifyGoalToolFence(ctx, tx, options.ChatID, options.Fence); err != nil {
			return err
		}
		current, err := CurrentChatGoalByRootChatID(ctx, tx, options.RootChatID)
		if err != nil {
			return err
		}
		if current.ID != goalID {
			return sql.ErrNoRows
		}
		if current.Status != database.ChatGoalStatusActive {
			return errGoalNotActive
		}
		if len(current.Objective)+textLen > codersdk.MaxChatGoalTextPayloadBytes {
			return errGoalTextPayloadTooLong
		}
		updated, err = update(tx)
		if err != nil {
			return err
		}
		chat, err = tx.GetChatByID(ctx, options.ChatID)
		return err
	}, nil)
	return updated, chat, err
}

// CurrentChatGoalByRootChatID returns the current goal for rootChatID, or
// sql.ErrNoRows when no current goal exists.
func CurrentChatGoalByRootChatID(ctx context.Context, db database.Store, rootChatID uuid.UUID) (database.ChatGoal, error) {
	goals, err := db.GetCurrentChatGoalsByRootChatIDs(ctx, []uuid.UUID{rootChatID})
	if err != nil {
		return database.ChatGoal{}, err
	}
	if len(goals) == 0 {
		return database.ChatGoal{}, sql.ErrNoRows
	}
	return goals[0], nil
}

// GetGoal returns a read-only tool for inspecting the current root goal.
func GetGoal(db database.Store, options GoalToolOptions) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		GetGoalToolName,
		"Inspect the current durable goal for this root chat. Returns null when no current goal exists.",
		func(ctx context.Context, _ getGoalArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			goal, err := CurrentChatGoalByRootChatID(ctx, db, options.RootChatID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return marshalToolResponse(goalResult{}), nil
				}
				return fantasy.NewTextErrorResponse("get goal: " + err.Error()), nil
			}
			sdkGoal := chatgoal.ToSDK(goal)
			return marshalToolResponse(goalResult{Goal: &sdkGoal}), nil
		},
	)
}

// CompleteGoal returns a root-only tool that marks the active goal complete.
func CompleteGoal(db database.Store, options GoalToolOptions) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		CompleteGoalToolName,
		"Mark the active chat goal complete after the objective is done. Requires the current goal_id and a concise completion summary. Only use this when the active goal has been satisfied.",
		func(ctx context.Context, args completeGoalArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if !options.IsRootChat {
				return fantasy.NewTextErrorResponse("complete_goal can only be used from the root chat"), nil
			}
			goalID, ok := parseGoalIDArg(args.GoalID)
			if !ok {
				return fantasy.NewTextErrorResponse("goal_id is required"), nil
			}
			summary := strings.TrimSpace(args.Summary)
			if summary == "" {
				return fantasy.NewTextErrorResponse("summary is required"), nil
			}
			if len(summary) > codersdk.MaxChatGoalCompletionSummaryBytes {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"summary must be at most %d bytes",
					codersdk.MaxChatGoalCompletionSummaryBytes,
				)), nil
			}

			completed, chat, err := mutateCurrentActiveGoal(ctx, db, options, goalID, len(summary), func(tx database.Store) (database.ChatGoal, error) {
				return tx.CompleteChatGoalByID(ctx, database.CompleteChatGoalByIDParams{
					RootChatID: options.RootChatID,
					ID:         goalID,
					CompletionSummary: sql.NullString{
						String: summary,
						Valid:  true,
					},
					CompletedByUserID: uuid.NullUUID{},
					CompletedByAgent:  true,
				})
			})
			if err != nil {
				switch {
				case errors.Is(err, sql.ErrNoRows):
					return fantasy.NewTextErrorResponse("current active goal does not match goal_id"), nil
				case errors.Is(err, errGoalTextPayloadTooLong):
					return fantasy.NewTextErrorResponse(fmt.Sprintf(
						"goal objective and summary must be at most %d bytes combined",
						codersdk.MaxChatGoalTextPayloadBytes,
					)), nil
				case errors.Is(err, errGoalNotActive):
					return fantasy.NewTextErrorResponse("current goal is not active"), nil
				case errors.Is(err, errGoalFenceMismatch):
					return fantasy.NewTextErrorResponse("the chat turn changed before the goal could be completed; the goal was not modified"), nil
				default:
					return fantasy.NewTextErrorResponse("complete goal: " + err.Error()), nil
				}
			}

			if options.OnGoalUpdated != nil {
				options.OnGoalUpdated(ctx, chat, completed)
			}
			sdkGoal := chatgoal.ToSDK(completed)
			return marshalToolResponse(completeGoalResult{
				Goal:      &sdkGoal,
				Completed: true,
				Summary:   summary,
			}), nil
		},
	)
}

// BlockGoal returns a root-only tool that marks the active goal blocked
// on user input. It is the model's escape hatch from the goal
// continuation loop: a blocked goal stops auto-continuation until the
// user resumes it.
func BlockGoal(db database.Store, options GoalToolOptions) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		BlockGoalToolName,
		"Mark the active chat goal blocked when you cannot proceed without the user, or you are stuck on the same obstacle repeatedly. Requires the current goal_id and a concise reason. Automatic goal continuation stops until the user resumes the goal.",
		func(ctx context.Context, args blockGoalArgs, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if !options.IsRootChat {
				return fantasy.NewTextErrorResponse("block_goal can only be used from the root chat"), nil
			}
			goalID, ok := parseGoalIDArg(args.GoalID)
			if !ok {
				return fantasy.NewTextErrorResponse("goal_id is required"), nil
			}
			reason := strings.TrimSpace(args.Reason)
			if reason == "" {
				return fantasy.NewTextErrorResponse("reason is required"), nil
			}
			if len(reason) > codersdk.MaxChatGoalBlockedReasonBytes {
				return fantasy.NewTextErrorResponse(fmt.Sprintf(
					"reason must be at most %d bytes",
					codersdk.MaxChatGoalBlockedReasonBytes,
				)), nil
			}

			blocked, chat, err := mutateCurrentActiveGoal(ctx, db, options, goalID, len(reason), func(tx database.Store) (database.ChatGoal, error) {
				return tx.BlockChatGoalByID(ctx, database.BlockChatGoalByIDParams{
					RootChatID:    options.RootChatID,
					ID:            goalID,
					BlockedReason: reason,
				})
			})
			if err != nil {
				switch {
				case errors.Is(err, sql.ErrNoRows):
					return fantasy.NewTextErrorResponse("current active goal does not match goal_id"), nil
				case errors.Is(err, errGoalTextPayloadTooLong):
					return fantasy.NewTextErrorResponse(fmt.Sprintf(
						"goal objective and reason must be at most %d bytes combined",
						codersdk.MaxChatGoalTextPayloadBytes,
					)), nil
				case errors.Is(err, errGoalNotActive):
					return fantasy.NewTextErrorResponse("current goal is not active"), nil
				case errors.Is(err, errGoalFenceMismatch):
					return fantasy.NewTextErrorResponse("the chat turn changed before the goal could be blocked; the goal was not modified"), nil
				default:
					return fantasy.NewTextErrorResponse("block goal: " + err.Error()), nil
				}
			}

			if options.OnGoalUpdated != nil {
				options.OnGoalUpdated(ctx, chat, blocked)
			}
			sdkGoal := chatgoal.ToSDK(blocked)
			return marshalToolResponse(blockGoalResult{
				Goal:    &sdkGoal,
				Blocked: true,
				Reason:  reason,
			}), nil
		},
	)
}

var (
	errGoalNotActive          = xerrors.New("goal is not active")
	errGoalTextPayloadTooLong = xerrors.New("goal text payload too long")
)
