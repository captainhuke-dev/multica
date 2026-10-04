package handler

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	externalPresenceSchema       = "controltower.execution_presence.v1"
	defaultExternalPresenceTTL   = 45 * time.Second
	maxExternalPresenceRows      = 500
	maxExternalPresenceBodyBytes = 1 << 20
)

type ExternalPresenceRow struct {
	IssueID         string `json:"issue_id"`
	IssueIdentifier string `json:"issue_identifier"`
	ExecutorRef     string `json:"executor_ref,omitempty"`
	Kind            string `json:"kind"`
	Source          string `json:"source"`
	RunID           string `json:"run_id,omitempty"`
	Generation      *int64 `json:"generation,omitempty"`
	PipelineState   string `json:"pipeline_state,omitempty"`
	ActivityState   string `json:"activity_state"`
	AcquiredAt      string `json:"acquired_at,omitempty"`
	HeartbeatAt     string `json:"heartbeat_at,omitempty"`
	Host            string `json:"host,omitempty"`
}

type externalPresenceInputRow struct {
	IssueID       string `json:"issue_id"`
	ExecutorRef   string `json:"executor_ref,omitempty"`
	Kind          string `json:"kind"`
	Source        string `json:"source"`
	RunID         string `json:"run_id,omitempty"`
	Generation    *int64 `json:"generation,omitempty"`
	PipelineState string `json:"pipeline_state,omitempty"`
	ActivityState string `json:"activity_state"`
	AcquiredAt    string `json:"acquired_at,omitempty"`
	HeartbeatAt   string `json:"heartbeat_at,omitempty"`
	Host          string `json:"host,omitempty"`
}

type externalPresenceReport struct {
	Status   string                     `json:"status"`
	Schema   string                     `json:"schema"`
	Presence []externalPresenceInputRow `json:"presence"`
}

type workspaceExternalPresenceResponse struct {
	Status   string                `json:"status"`
	Presence []ExternalPresenceRow `json:"presence"`
}

type externalPresenceSnapshot struct {
	updatedAt time.Time
	available bool
	rows      []ExternalPresenceRow
}

type ExternalPresenceStore struct {
	mu          sync.Mutex
	ttl         time.Duration
	byWorkspace map[string]map[string]externalPresenceSnapshot
}

func NewExternalPresenceStore(ttl time.Duration) *ExternalPresenceStore {
	if ttl <= 0 {
		ttl = defaultExternalPresenceTTL
	}
	return &ExternalPresenceStore{
		ttl:         ttl,
		byWorkspace: make(map[string]map[string]externalPresenceSnapshot),
	}
}

func (s *ExternalPresenceStore) Replace(
	workspaceID string,
	runtimeID string,
	rows []ExternalPresenceRow,
	available bool,
	now time.Time,
) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := s.byWorkspace[workspaceID]
	if workspace == nil {
		workspace = make(map[string]externalPresenceSnapshot)
		s.byWorkspace[workspaceID] = workspace
	}
	copied := append([]ExternalPresenceRow(nil), rows...)
	workspace[runtimeID] = externalPresenceSnapshot{
		updatedAt: now,
		available: available,
		rows:      copied,
	}
}

func (s *ExternalPresenceStore) List(
	workspaceID string,
	now time.Time,
) ([]ExternalPresenceRow, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	workspace := s.byWorkspace[workspaceID]
	if len(workspace) == 0 {
		return nil, false
	}

	available := false
	deduped := make(map[string]ExternalPresenceRow)
	for runtimeID, snapshot := range workspace {
		if now.Sub(snapshot.updatedAt) > s.ttl {
			delete(workspace, runtimeID)
			continue
		}
		if !snapshot.available {
			continue
		}
		available = true
		for _, row := range snapshot.rows {
			key := row.IssueID + "\x00" + row.ExecutorRef + "\x00" + row.RunID + "\x00" + row.ActivityState
			deduped[key] = row
		}
	}
	if len(workspace) == 0 {
		delete(s.byWorkspace, workspaceID)
	}
	if !available {
		return nil, false
	}

	rows := make([]ExternalPresenceRow, 0, len(deduped))
	for _, row := range deduped {
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b ExternalPresenceRow) int {
		if c := strings.Compare(a.IssueIdentifier, b.IssueIdentifier); c != 0 {
			return c
		}
		if c := strings.Compare(a.ExecutorRef, b.ExecutorRef); c != 0 {
			return c
		}
		return strings.Compare(a.RunID, b.RunID)
	})
	return rows, true
}

func validExternalPresenceActivity(value string) bool {
	switch value {
	case "active", "waiting", "blocked", "stale", "unknown", "inactive":
		return true
	default:
		return false
	}
}

func (h *Handler) ReportExternalPresence(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	workspaceID := uuidToString(runtime.WorkspaceID)

	r.Body = http.MaxBytesReader(w, r.Body, maxExternalPresenceBodyBytes)
	var req externalPresenceReport
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Schema != externalPresenceSchema {
		writeError(w, http.StatusBadRequest, "unsupported external presence schema")
		return
	}
	if req.Status != "ok" && req.Status != "unavailable" {
		writeError(w, http.StatusBadRequest, "invalid external presence status")
		return
	}
	if req.Status == "unavailable" {
		h.ExternalPresence.Replace(workspaceID, runtimeID, nil, false, time.Now())
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"count":  0,
		})
		return
	}
	if len(req.Presence) > maxExternalPresenceRows {
		writeError(w, http.StatusBadRequest, "too many external presence rows")
		return
	}

	rows := make([]ExternalPresenceRow, 0, len(req.Presence))
	for _, input := range req.Presence {
		identifier := strings.TrimSpace(input.IssueID)
		if identifier == "" || len(identifier) > 128 {
			writeError(w, http.StatusBadRequest, "invalid external presence issue_id")
			return
		}
		if !validExternalPresenceActivity(input.ActivityState) {
			writeError(w, http.StatusBadRequest, "invalid external presence activity_state")
			return
		}
		input.ExecutorRef = strings.TrimSpace(input.ExecutorRef)
		input.Kind = strings.TrimSpace(input.Kind)
		input.Source = strings.TrimSpace(input.Source)
		input.RunID = strings.TrimSpace(input.RunID)
		input.PipelineState = strings.TrimSpace(input.PipelineState)
		input.AcquiredAt = strings.TrimSpace(input.AcquiredAt)
		input.HeartbeatAt = strings.TrimSpace(input.HeartbeatAt)
		input.Host = strings.TrimSpace(input.Host)
		if input.Kind == "" || input.Source == "" {
			writeError(w, http.StatusBadRequest, "external presence kind and source are required")
			return
		}
		if input.ActivityState == "active" && input.ExecutorRef == "" {
			writeError(w, http.StatusBadRequest, "active external presence requires executor_ref")
			return
		}
		if len(input.ExecutorRef) > 256 ||
			len(input.Kind) > 64 ||
			len(input.Source) > 128 ||
			len(input.RunID) > 256 ||
			len(input.PipelineState) > 64 ||
			len(input.AcquiredAt) > 64 ||
			len(input.HeartbeatAt) > 64 ||
			len(input.Host) > 256 {
			writeError(w, http.StatusBadRequest, "external presence field too long")
			return
		}

		issue, found := h.resolveIssueByIdentifier(r.Context(), identifier, workspaceID)
		if !found {
			// The daemon can serve multiple workspaces while the host-local
			// projection is global. Drop identifiers that do not belong to this
			// runtime's workspace rather than leaking them across workspaces.
			continue
		}
		rows = append(rows, ExternalPresenceRow{
			IssueID:         uuidToString(issue.ID),
			IssueIdentifier: identifier,
			ExecutorRef:     strings.TrimSpace(input.ExecutorRef),
			Kind:            strings.TrimSpace(input.Kind),
			Source:          strings.TrimSpace(input.Source),
			RunID:           strings.TrimSpace(input.RunID),
			Generation:      input.Generation,
			PipelineState:   strings.TrimSpace(input.PipelineState),
			ActivityState:   input.ActivityState,
			AcquiredAt:      strings.TrimSpace(input.AcquiredAt),
			HeartbeatAt:     strings.TrimSpace(input.HeartbeatAt),
			Host:            strings.TrimSpace(input.Host),
		})
	}

	h.ExternalPresence.Replace(workspaceID, runtimeID, rows, true, time.Now())
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"count":  len(rows),
	})
}

func (h *Handler) ListWorkspaceExternalPresence(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	rows, available := h.ExternalPresence.List(workspaceID, time.Now())
	if !available {
		writeJSON(w, http.StatusOK, workspaceExternalPresenceResponse{
			Status:   "unavailable",
			Presence: []ExternalPresenceRow{},
		})
		return
	}
	writeJSON(w, http.StatusOK, workspaceExternalPresenceResponse{
		Status:   "ok",
		Presence: rows,
	})
}
