package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func externalPresenceRow(issueID, executor, runID, activity string) ExternalPresenceRow {
	return ExternalPresenceRow{
		IssueID:         issueID,
		IssueIdentifier: "MCIT-508",
		ExecutorRef:     executor,
		Kind:            "controltower_executor",
		Source:          "controltower_execution_lease",
		RunID:           runID,
		ActivityState:   activity,
	}
}

func TestExternalPresenceStoreEmptyFreshSnapshotIsAvailable(t *testing.T) {
	store := NewExternalPresenceStore(45 * time.Second)
	now := time.Unix(1000, 0)
	store.Replace("ws-1", "runtime-1", nil, true, now)

	rows, available := store.List("ws-1", now.Add(10*time.Second))
	if !available {
		t.Fatal("fresh empty snapshot must be available")
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %#v, want empty", rows)
	}
}

func TestExternalPresenceStoreUnavailableReportOverridesFreshActiveSnapshot(t *testing.T) {
	store := NewExternalPresenceStore(45 * time.Second)
	now := time.Unix(1000, 0)
	store.Replace("ws-1", "runtime-1", []ExternalPresenceRow{
		externalPresenceRow("issue-1", "gpt-sol", "run-1", "active"),
	}, true, now)
	store.Replace("ws-1", "runtime-1", nil, false, now.Add(time.Second))

	rows, available := store.List("ws-1", now.Add(2*time.Second))
	if available {
		t.Fatal("fresh unavailable report must not preserve prior active snapshot")
	}
	if rows != nil {
		t.Fatalf("rows = %#v, want nil", rows)
	}
}

func TestExternalPresenceStoreExpiredSnapshotIsUnavailable(t *testing.T) {
	store := NewExternalPresenceStore(45 * time.Second)
	now := time.Unix(1000, 0)
	store.Replace("ws-1", "runtime-1", []ExternalPresenceRow{
		externalPresenceRow("issue-1", "gpt-sol", "run-1", "active"),
	}, true, now)

	rows, available := store.List("ws-1", now.Add(46*time.Second))
	if available {
		t.Fatal("expired snapshot must be unavailable")
	}
	if rows != nil {
		t.Fatalf("rows = %#v, want nil", rows)
	}
}

func TestExternalPresenceStoreDedupesAcrossRuntimes(t *testing.T) {
	store := NewExternalPresenceStore(45 * time.Second)
	now := time.Unix(1000, 0)
	row := externalPresenceRow("issue-1", "gpt-sol", "run-1", "active")
	store.Replace("ws-1", "runtime-1", []ExternalPresenceRow{row}, true, now)
	store.Replace("ws-1", "runtime-2", []ExternalPresenceRow{row}, true, now)

	rows, available := store.List("ws-1", now.Add(10*time.Second))
	if !available {
		t.Fatal("snapshot must be available")
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %#v, want one deduped row", rows)
	}
}

func TestExternalPresenceStorePreservesSourceStaleStateWhileTransportFresh(t *testing.T) {
	store := NewExternalPresenceStore(45 * time.Second)
	now := time.Unix(1000, 0)
	store.Replace("ws-1", "runtime-1", []ExternalPresenceRow{
		externalPresenceRow("issue-1", "gpt-sol", "run-1", "stale"),
	}, true, now)

	rows, available := store.List("ws-1", now.Add(10*time.Second))
	if !available {
		t.Fatal("fresh transport snapshot must be available")
	}
	if len(rows) != 1 || rows[0].ActivityState != "stale" {
		t.Fatalf("rows = %#v, want stale source evidence", rows)
	}
}

func TestExternalPresenceStoreWorkspaceIsolation(t *testing.T) {
	store := NewExternalPresenceStore(45 * time.Second)
	now := time.Unix(1000, 0)
	store.Replace("ws-1", "runtime-1", []ExternalPresenceRow{
		externalPresenceRow("issue-1", "gpt-sol", "run-1", "active"),
	}, true, now)

	rows, available := store.List("ws-2", now)
	if available || rows != nil {
		t.Fatalf("other workspace saw presence: available=%v rows=%#v", available, rows)
	}
}

func TestValidExternalPresenceActivity(t *testing.T) {
	for _, value := range []string{"active", "waiting", "blocked", "stale", "unknown", "inactive"} {
		if !validExternalPresenceActivity(value) {
			t.Fatalf("%q must be valid", value)
		}
	}
	if validExternalPresenceActivity("working-ish") {
		t.Fatal("unexpected activity accepted")
	}
}

func TestReportExternalPresenceResolvesWorkspaceIssueAndListsIt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	h := *testHandler
	h.ExternalPresence = NewExternalPresenceStore(time.Minute)
	issue := createIssueForTest(t, map[string]any{
		"title":  "external presence resolution",
		"status": "todo",
	})

	report := externalPresenceReport{
		Status: "ok",
		Schema: externalPresenceSchema,
		Presence: []externalPresenceInputRow{
			{
				IssueID:       issue.Identifier,
				ExecutorRef:   "gpt-sol",
				Kind:          "controltower_executor",
				Source:        "controltower_execution_lease",
				RunID:         "run-external-1",
				PipelineState: "EXECUTING",
				ActivityState: "active",
				Host:          "chatgpt-desktop",
			},
		},
	}
	reportReq := withURLParam(
		newRequest(http.MethodPost, "/api/daemon/runtimes/"+testRuntimeID+"/external-presence", report),
		"runtimeId",
		testRuntimeID,
	)
	reportW := httptest.NewRecorder()
	h.ReportExternalPresence(reportW, reportReq)
	if reportW.Code != http.StatusOK {
		t.Fatalf("report expected 200, got %d: %s", reportW.Code, reportW.Body.String())
	}

	var accepted map[string]any
	if err := json.NewDecoder(reportW.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode report response: %v", err)
	}
	if got := int(accepted["count"].(float64)); got != 1 {
		t.Fatalf("accepted count = %d, want 1", got)
	}

	listW := httptest.NewRecorder()
	h.ListWorkspaceExternalPresence(
		listW,
		newRequest(http.MethodGet, "/api/external-presence", nil),
	)
	if listW.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d: %s", listW.Code, listW.Body.String())
	}
	var listed workspaceExternalPresenceResponse
	if err := json.NewDecoder(listW.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listed.Status != "ok" || len(listed.Presence) != 1 {
		t.Fatalf("listed = %#v, want one available row", listed)
	}
	row := listed.Presence[0]
	if row.IssueID != issue.ID || row.IssueIdentifier != issue.Identifier {
		t.Fatalf("issue resolution = (%q,%q), want (%q,%q)", row.IssueID, row.IssueIdentifier, issue.ID, issue.Identifier)
	}
	if row.ExecutorRef != "gpt-sol" || row.ActivityState != "active" || row.RunID != "run-external-1" {
		t.Fatalf("unexpected row: %#v", row)
	}
}

func TestReportExternalPresenceDropsIdentifierOutsideRuntimeWorkspace(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	h := *testHandler
	h.ExternalPresence = NewExternalPresenceStore(time.Minute)

	report := externalPresenceReport{
		Status: "ok",
		Schema: externalPresenceSchema,
		Presence: []externalPresenceInputRow{
			{
				IssueID:       "OTHER-999999",
				ExecutorRef:   "do-not-leak",
				Kind:          "controltower_executor",
				Source:        "controltower_execution_lease",
				ActivityState: "active",
			},
		},
	}
	req := withURLParam(
		newRequest(http.MethodPost, "/api/daemon/runtimes/"+testRuntimeID+"/external-presence", report),
		"runtimeId",
		testRuntimeID,
	)
	w := httptest.NewRecorder()
	h.ReportExternalPresence(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("report expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var accepted map[string]any
	if err := json.NewDecoder(w.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := int(accepted["count"].(float64)); got != 0 {
		t.Fatalf("accepted count = %d, want 0", got)
	}

	rows, available := h.ExternalPresence.List(testWorkspaceID, time.Now())
	if !available {
		t.Fatal("fresh empty snapshot must be available after a valid report")
	}
	if len(rows) != 0 {
		t.Fatalf("cross-workspace identifier leaked into snapshot: %#v", rows)
	}
}

func TestListWorkspaceExternalPresenceUnavailableWithoutFreshReport(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	h := *testHandler
	h.ExternalPresence = NewExternalPresenceStore(time.Minute)

	w := httptest.NewRecorder()
	h.ListWorkspaceExternalPresence(
		w,
		newRequest(http.MethodGet, "/api/external-presence", nil),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response workspaceExternalPresenceResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "unavailable" || len(response.Presence) != 0 {
		t.Fatalf("response = %#v, want unavailable with no rows", response)
	}
}

func TestReportExternalPresenceRejectsAmbiguousActiveRows(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	for _, tc := range []struct {
		name string
		row  externalPresenceInputRow
	}{
		{
			name: "active missing executor",
			row: externalPresenceInputRow{
				IssueID:       "HAN-1",
				Kind:          "controltower_executor",
				Source:        "controltower_execution_lease",
				ActivityState: "active",
			},
		},
		{
			name: "missing kind",
			row: externalPresenceInputRow{
				IssueID:       "HAN-1",
				ExecutorRef:   "gpt-sol",
				Source:        "controltower_execution_lease",
				ActivityState: "active",
			},
		},
		{
			name: "missing source",
			row: externalPresenceInputRow{
				IssueID:       "HAN-1",
				ExecutorRef:   "gpt-sol",
				Kind:          "controltower_executor",
				ActivityState: "active",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := *testHandler
			h.ExternalPresence = NewExternalPresenceStore(time.Minute)
			req := withURLParam(
				newRequest(http.MethodPost, "/api/daemon/runtimes/"+testRuntimeID+"/external-presence", externalPresenceReport{
					Schema:   externalPresenceSchema,
					Presence: []externalPresenceInputRow{tc.row},
				}),
				"runtimeId",
				testRuntimeID,
			)
			w := httptest.NewRecorder()
			h.ReportExternalPresence(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
