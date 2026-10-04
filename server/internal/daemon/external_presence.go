package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	externalPresenceSchema  = "controltower.execution_presence.v1"
	externalPresenceMaxRows = 500
	externalPresenceMaxBody = 1 << 20
)

type ExternalPresenceRecord struct {
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

type externalPresenceEnvelope struct {
	Status   string                   `json:"status"`
	Schema   string                   `json:"schema"`
	Presence []ExternalPresenceRecord `json:"presence"`
}

type ExternalPresenceReport struct {
	Status   string                   `json:"status"`
	Schema   string                   `json:"schema"`
	Presence []ExternalPresenceRecord `json:"presence"`
}

type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > b.max {
		return 0, fmt.Errorf("external presence output exceeds %d bytes", b.max)
	}
	return b.buf.Write(p)
}

func (b *cappedBuffer) Bytes() []byte {
	return b.buf.Bytes()
}

func parseExternalPresencePayload(data []byte) ([]ExternalPresenceRecord, error) {
	var envelope externalPresenceEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode external presence: %w", err)
	}
	if envelope.Status != "ok" {
		return nil, fmt.Errorf("external presence status is %q", envelope.Status)
	}
	if envelope.Schema != externalPresenceSchema {
		return nil, fmt.Errorf("unsupported external presence schema %q", envelope.Schema)
	}
	if len(envelope.Presence) > externalPresenceMaxRows {
		return nil, fmt.Errorf("external presence row count %d exceeds %d", len(envelope.Presence), externalPresenceMaxRows)
	}
	for i, row := range envelope.Presence {
		if strings.TrimSpace(row.IssueID) == "" {
			return nil, fmt.Errorf("external presence row %d missing issue_id", i)
		}
		switch row.ActivityState {
		case "active", "waiting", "blocked", "stale", "unknown", "inactive":
		default:
			return nil, fmt.Errorf("external presence row %d has invalid activity_state %q", i, row.ActivityState)
		}
	}
	return envelope.Presence, nil
}

func (d *Daemon) collectExternalPresence(ctx context.Context) ([]ExternalPresenceRecord, error) {
	if strings.TrimSpace(d.cfg.ExternalPresenceCommand) == "" {
		return nil, nil
	}
	timeout := d.cfg.ExternalPresenceTimeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, d.cfg.ExternalPresenceCommand, d.cfg.ExternalPresenceArgs...)
	cmd.Env = os.Environ()
	var stdout cappedBuffer
	stdout.max = externalPresenceMaxBody
	var stderr cappedBuffer
	stderr.max = 32 << 10
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if runCtx.Err() != nil {
			return nil, runCtx.Err()
		}
		return nil, fmt.Errorf("external presence command failed: %w", err)
	}
	return parseExternalPresencePayload(stdout.Bytes())
}

func (d *Daemon) externalPresenceRuntimeIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	ids := make([]string, 0, len(d.workspaces))
	for _, ws := range d.workspaces {
		if ws == nil {
			continue
		}
		ids = append(ids, ws.runtimeIDs...)
	}
	return ids
}

func (d *Daemon) reportExternalPresence(ctx context.Context) {
	rows, err := d.collectExternalPresence(ctx)
	report := ExternalPresenceReport{
		Status: "ok",
		Schema: externalPresenceSchema,
	}
	if err != nil {
		report.Status = "unavailable"
		if ctx.Err() == nil {
			d.logger.Warn("external presence collection failed", "error", err)
		}
	} else {
		report.Presence = rows
	}
	for _, runtimeID := range d.externalPresenceRuntimeIDs() {
		reportCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := d.client.ReportExternalPresence(reportCtx, runtimeID, report)
		cancel()
		if err != nil && ctx.Err() == nil {
			d.logger.Warn("external presence report failed", "runtime_id", runtimeID, "error", err)
		}
	}
}

func (d *Daemon) externalPresenceLoop(ctx context.Context) {
	if strings.TrimSpace(d.cfg.ExternalPresenceCommand) == "" {
		return
	}
	interval := d.cfg.ExternalPresenceInterval
	if interval <= 0 {
		interval = d.cfg.HeartbeatInterval
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}

	d.logger.Info(
		"external presence collector enabled",
		"interval", interval,
		"timeout", d.cfg.ExternalPresenceTimeout,
	)
	d.reportExternalPresence(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.reportExternalPresence(ctx)
		}
	}
}
