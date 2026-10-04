package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// taskCompleteStoredResult is server-owned persistence metadata layered on top
// of the daemon request. The daemon cannot set ResponseEngineEnforced because
// it is not part of TaskCompleteRequest; the server mints it only after a
// trusted Response Engine finalization succeeds.
type taskCompleteStoredResult struct {
	TaskCompleteRequest
	ResponseEngineEnforced bool `json:"response_engine_enforced,omitempty"`
}

func (h *Handler) finalizeTaskCompletionResponse(
	w http.ResponseWriter,
	r *http.Request,
	taskID string,
	claimedTask db.AgentTaskQueue,
	workspaceID string,
	req *TaskCompleteRequest,
) (*service.TaskResponseCompletionFence, bool) {
	if claimedTask.Status != "running" {
		return nil, true
	}

	finalized, err := h.TaskService.FinalizeCompletionOutputWithFence(r.Context(), claimedTask, workspaceID, req.Output)
	if err != nil {
		slog.Warn("complete task response finalization failed",
			"task_id", taskID,
			"error", err,
		)
		// Finalization-plane failures must not be rewritten as execution failures.
		// A 503 makes the daemon retry the terminal callback without re-running the
		// agent and without persisting the raw model/harness output.
		writeError(w, http.StatusServiceUnavailable, "response finalization unavailable")
		return nil, false
	}
	req.Output = finalized.Output
	return finalized.Fence, true
}

func marshalTaskCompleteStoredResult(req TaskCompleteRequest, responseFence *service.TaskResponseCompletionFence) []byte {
	result, _ := json.Marshal(taskCompleteStoredResult{
		TaskCompleteRequest:    req,
		ResponseEngineEnforced: responseFence != nil,
	})
	return result
}
