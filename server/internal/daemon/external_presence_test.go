package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func validExternalPresenceJSON(t *testing.T, rows []ExternalPresenceRecord) []byte {
	t.Helper()
	payload := externalPresenceEnvelope{
		Status:   "ok",
		Schema:   externalPresenceSchema,
		Presence: rows,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseExternalPresencePayloadValid(t *testing.T) {
	rows, err := parseExternalPresencePayload(validExternalPresenceJSON(t, []ExternalPresenceRecord{
		{
			IssueID:       "MCIT-508",
			ExecutorRef:   "gpt-sol",
			Kind:          "controltower_executor",
			Source:        "controltower_execution_lease",
			ActivityState: "active",
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].IssueID != "MCIT-508" || rows[0].ActivityState != "active" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

func TestParseExternalPresencePayloadRejectsSchema(t *testing.T) {
	payload := externalPresenceEnvelope{
		Status: "ok",
		Schema: "wrong",
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseExternalPresencePayload(data); err == nil {
		t.Fatal("expected schema error")
	}
}

func TestParseExternalPresencePayloadRejectsInvalidActivity(t *testing.T) {
	_, err := parseExternalPresencePayload(validExternalPresenceJSON(t, []ExternalPresenceRecord{
		{IssueID: "MCIT-508", ActivityState: "guessing"},
	}))
	if err == nil {
		t.Fatal("expected activity error")
	}
}

func TestParseExternalPresencePayloadRejectsEmptyIssueID(t *testing.T) {
	_, err := parseExternalPresencePayload(validExternalPresenceJSON(t, []ExternalPresenceRecord{
		{IssueID: " ", ActivityState: "active"},
	}))
	if err == nil {
		t.Fatal("expected issue id error")
	}
}

func TestParseExternalPresencePayloadRejectsTooManyRows(t *testing.T) {
	rows := make([]ExternalPresenceRecord, externalPresenceMaxRows+1)
	for i := range rows {
		rows[i] = ExternalPresenceRecord{
			IssueID:       fmt.Sprintf("MCIT-%d", i+1),
			ActivityState: "active",
		}
	}
	if _, err := parseExternalPresencePayload(validExternalPresenceJSON(t, rows)); err == nil {
		t.Fatal("expected row count error")
	}
}

func TestExternalPresenceRuntimeIDsIncludesAllWorkspaceRuntimes(t *testing.T) {
	d := &Daemon{
		workspaces: map[string]*workspaceState{
			"ws-a": {workspaceID: "ws-a", runtimeIDs: []string{"runtime-a1", "runtime-a2"}},
			"ws-b": {workspaceID: "ws-b", runtimeIDs: []string{"runtime-b1"}},
			"ws-c": {workspaceID: "ws-c"},
		},
	}
	got := d.externalPresenceRuntimeIDs()
	if len(got) != 3 {
		t.Fatalf("runtime count = %d, want 3: %#v", len(got), got)
	}
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	for _, id := range []string{"runtime-a1", "runtime-a2", "runtime-b1"} {
		if !seen[id] {
			t.Fatalf("missing runtime %q from %#v", id, got)
		}
	}
}

func TestExternalPresenceCommandHelper(t *testing.T) {
	if os.Getenv("GO_WANT_EXTERNAL_PRESENCE_HELPER") != "1" {
		return
	}
	_, _ = fmt.Fprint(os.Stdout, `{"status":"ok","schema":"controltower.execution_presence.v1","presence":[{"issue_id":"MCIT-508","executor_ref":"gpt-sol","kind":"controltower_executor","source":"controltower_execution_lease","run_id":"run-1","activity_state":"active"}]}`)
	os.Exit(0)
}

func TestCollectExternalPresenceRunsConfiguredCommand(t *testing.T) {
	t.Setenv("GO_WANT_EXTERNAL_PRESENCE_HELPER", "1")
	d := &Daemon{
		cfg: Config{
			ExternalPresenceCommand: os.Args[0],
			ExternalPresenceArgs:    []string{"-test.run=TestExternalPresenceCommandHelper"},
			ExternalPresenceTimeout: 5 * time.Second,
		},
	}
	rows, err := d.collectExternalPresence(context.Background())
	if err != nil {
		t.Fatalf("collectExternalPresence: %v", err)
	}
	if len(rows) != 1 || rows[0].IssueID != "MCIT-508" || rows[0].ExecutorRef != "gpt-sol" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
}

func TestCollectExternalPresenceDisabledIsEmpty(t *testing.T) {
	d := &Daemon{}
	rows, err := d.collectExternalPresence(context.Background())
	if err != nil {
		t.Fatalf("collectExternalPresence: %v", err)
	}
	if rows != nil {
		t.Fatalf("rows = %#v, want nil", rows)
	}
}

func TestClientReportExternalPresenceUsesDaemonAuthAndRuntimePath(t *testing.T) {
	var got ExternalPresenceReport
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/daemon/runtimes/runtime-1/external-presence" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer daemon-token" {
			t.Errorf("authorization = %q", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode report: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	client.SetToken("daemon-token")
	want := ExternalPresenceReport{
		Status: "ok",
		Schema: externalPresenceSchema,
		Presence: []ExternalPresenceRecord{
			{
				IssueID:       "MCIT-508",
				ExecutorRef:   "gpt-sol",
				Kind:          "controltower_executor",
				Source:        "controltower_execution_lease",
				ActivityState: "active",
			},
		},
	}
	if err := client.ReportExternalPresence(context.Background(), "runtime-1", want); err != nil {
		t.Fatalf("ReportExternalPresence: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("report = %#v, want %#v", got, want)
	}
}

func TestLoadConfigExternalPresenceFromEnv(t *testing.T) {
	stageFakeAgent(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "missing-shell"))
	t.Setenv("MULTICA_EXTERNAL_PRESENCE_COMMAND", "controltower-execution-presence")
	t.Setenv("MULTICA_EXTERNAL_PRESENCE_ARGS", "--working-only")
	t.Setenv("MULTICA_EXTERNAL_PRESENCE_INTERVAL", "20s")
	t.Setenv("MULTICA_EXTERNAL_PRESENCE_TIMEOUT", "2s")

	cfg, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:0",
		WorkspacesRoot: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ExternalPresenceCommand != "controltower-execution-presence" {
		t.Fatalf("command = %q", cfg.ExternalPresenceCommand)
	}
	if !reflect.DeepEqual(cfg.ExternalPresenceArgs, []string{"--working-only"}) {
		t.Fatalf("args = %#v", cfg.ExternalPresenceArgs)
	}
	if cfg.ExternalPresenceInterval != 20*time.Second {
		t.Fatalf("interval = %s", cfg.ExternalPresenceInterval)
	}
	if cfg.ExternalPresenceTimeout != 2*time.Second {
		t.Fatalf("timeout = %s", cfg.ExternalPresenceTimeout)
	}
}

func TestLoadConfigExternalPresenceRejectsMalformedArgs(t *testing.T) {
	stageFakeAgent(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "missing-shell"))
	t.Setenv("MULTICA_EXTERNAL_PRESENCE_COMMAND", "controltower-execution-presence")
	t.Setenv("MULTICA_EXTERNAL_PRESENCE_ARGS", `"unterminated`)

	_, err := LoadConfig(Overrides{
		ServerURL:      "http://localhost:0",
		WorkspacesRoot: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected malformed args error")
	}
}
