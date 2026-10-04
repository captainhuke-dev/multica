package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
)

type responseEnginePersistedCompletion struct {
	Output                 string `json:"output"`
	ResponseEngineEnforced bool   `json:"response_engine_enforced"`
}

func (s *TaskService) CompleteTaskWithResponseFence(ctx context.Context, taskID pgtype.UUID, result []byte, sessionID, workDir, branchName string, sessionRolloutMissing bool, retiredSessionID, durableWorkDir string, fence *TaskResponseCompletionFence) (*db.AgentTaskQueue, error) {
	task, _, err := s.completeTask(ctx, taskID, result, sessionID, workDir, branchName, sessionRolloutMissing, retiredSessionID, durableWorkDir, fence)
	return task, err
}

// CompleteTaskWithResponseFenceTransition combines the response snapshot fence
// with the running -> completed transition result used to suppress replayed
// transaction-external side effects.
func (s *TaskService) CompleteTaskWithResponseFenceTransition(ctx context.Context, taskID pgtype.UUID, result []byte, sessionID, workDir, branchName string, sessionRolloutMissing bool, retiredSessionID, durableWorkDir string, fence *TaskResponseCompletionFence) (*db.AgentTaskQueue, bool, error) {
	return s.completeTask(ctx, taskID, result, sessionID, workDir, branchName, sessionRolloutMissing, retiredSessionID, durableWorkDir, fence)
}

func (s *TaskService) validateResponseCompletionFence(ctx context.Context, qtx *db.Queries, fence *TaskResponseCompletionFence) error {
	if fence == nil {
		return nil
	}
	issue, err := qtx.LockIssueForDescriptionUpdate(ctx, db.LockIssueForDescriptionUpdateParams{
		ID:          fence.IssueID,
		WorkspaceID: fence.WorkspaceID,
	})
	if err != nil {
		return fmt.Errorf("%w: lock issue: %v", ErrResponseSnapshotStale, err)
	}
	if issue.Revision != fence.Revision {
		return fmt.Errorf("%w: issue revision changed from %d to %d", ErrResponseSnapshotStale, fence.Revision, issue.Revision)
	}
	return nil
}

func (s *TaskService) ensureResponseEngineTerminalComment(
	ctx context.Context,
	task db.AgentTaskQueue,
	result []byte,
) error {
	if !task.IssueID.Valid {
		return nil
	}

	var persisted responseEnginePersistedCompletion
	if err := json.Unmarshal(result, &persisted); err != nil {
		return fmt.Errorf("decode enforced terminal result: %w", err)
	}
	if !persisted.ResponseEngineEnforced {
		return nil
	}
	if strings.TrimSpace(persisted.Output) == "" {
		return errors.New("enforced terminal result is missing rendered output")
	}

	suppressNoActionComment, err := HasSquadLeaderNoActionEvaluationForTask(ctx, s.Queries, task)
	if err != nil {
		slog.Warn("checking squad leader no_action evaluation failed",
			"task_id", util.UUIDToString(task.ID),
			"issue_id", util.UUIDToString(task.IssueID),
			"agent_id", util.UUIDToString(task.AgentID),
			"error", err,
		)
	}
	if suppressNoActionComment {
		return nil
	}

	body := util.UnescapeBackslashEscapes(persisted.Output)
	content := redact.Text(body)
	if content == "" {
		return errors.New("enforced terminal rendered output became empty after sanitization")
	}

	err = s.createResponseEngineTerminalCommentWithID(
		ctx,
		task.ID,
		task.IssueID,
		task.AgentID,
		content,
		"comment",
		task.TriggerCommentID,
		task.ID,
	)
	if err == nil {
		return nil
	}

	issue, issueErr := s.Queries.GetIssue(ctx, task.IssueID)
	if issueErr != nil {
		return fmt.Errorf("create enforced terminal comment: %w", err)
	}
	existing, existingErr := s.Queries.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
		ID:          task.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if existingErr != nil {
		return fmt.Errorf("create enforced terminal comment: %w", err)
	}
	if existing.IssueID != task.IssueID ||
		existing.AuthorType != "agent" ||
		existing.AuthorID != task.AgentID ||
		existing.Content != content ||
		existing.Type != "comment" ||
		existing.ParentID != task.TriggerCommentID ||
		existing.SourceTaskID != task.ID {
		return errors.New("enforced terminal comment id collision")
	}
	return nil
}

func (s *TaskService) createResponseEngineTerminalCommentWithID(ctx context.Context, commentID, issueID, agentID pgtype.UUID, content, commentType string, parentID, sourceTaskID pgtype.UUID) error {
	if content == "" {
		return nil
	}
	issue, err := s.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return err
	}
	var rootComment *db.Comment
	if parentID.Valid {
		if root, err := s.Queries.GetThreadRoot(ctx, db.GetThreadRootParams{
			CommentID:   parentID,
			WorkspaceID: issue.WorkspaceID,
		}); err == nil {
			rootComment = &root
		}
	}
	created, err := s.Queries.CreateComment(ctx, db.CreateCommentParams{
		ID:           commentID,
		IssueID:      issueID,
		WorkspaceID:  issue.WorkspaceID,
		AuthorType:   "agent",
		AuthorID:     agentID,
		Content:      content,
		Type:         commentType,
		ParentID:     parentID,
		SourceTaskID: sourceTaskID,
	})
	if err != nil {
		return err
	}
	comment := created.Comment()
	commentFields := commentEventFields(comment)
	commentFields["revision"] = comment.Revision
	s.Bus.Publish(events.Event{
		Type:        protocol.EventCommentCreated,
		WorkspaceID: util.UUIDToString(issue.WorkspaceID),
		ActorType:   "agent",
		ActorID:     util.UUIDToString(agentID),
		Payload: map[string]any{
			"comment":        commentFields,
			"issue_title":    issue.Title,
			"issue_status":   issue.Status,
			"issue_revision": created.IssueRevision,
		},
	})
	s.AutoUnresolveThreadOnReply(ctx, rootComment, util.UUIDToString(issue.WorkspaceID), "agent", util.UUIDToString(agentID), sourceTaskID)
	return nil
}
