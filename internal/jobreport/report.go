// Package jobreport defines the optional, stdout-only progress protocol used
// by refresh workers. It has no Mongo, HTTP, or web-application dependency.
package jobreport

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

const Schema = "kerkerker.plugin-job.v1"

type Kind string

const (
	KindStarted  Kind = "started"
	KindProgress Kind = "progress"
	KindFinished Kind = "finished"
)

type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusPartial   Status = "partial"
	StatusFailed    Status = "failed"
)

type Metadata struct {
	RunID         string `json:"run_id"`
	PluginID      string `json:"plugin_id"`
	PluginVersion string `json:"plugin_version"`
	ProfileID     string `json:"profile_id"`
	ConfigVersion string `json:"config_version"`
	Actor         string `json:"actor"`
	Attempt       int    `json:"attempt"`
}

type Progress struct {
	Total     int `json:"total"`
	Processed int `json:"processed"`
	Created   int `json:"created"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
}

type Error struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type Event struct {
	Schema     string   `json:"schema"`
	Kind       Kind     `json:"kind"`
	OccurredAt string   `json:"occurred_at"`
	Metadata   Metadata `json:"metadata"`
	Status     Status   `json:"status"`
	Progress   Progress `json:"progress"`
	Error      *Error   `json:"error,omitempty"`
}

func (e Event) Validate() error {
	if e.Schema != Schema {
		return fmt.Errorf("unsupported job report schema %q", e.Schema)
	}
	if e.Kind != KindStarted && e.Kind != KindProgress && e.Kind != KindFinished {
		return fmt.Errorf("invalid job report kind %q", e.Kind)
	}
	if _, err := time.Parse(time.RFC3339Nano, e.OccurredAt); err != nil {
		return fmt.Errorf("occurred_at must be RFC3339: %w", err)
	}
	if strings.TrimSpace(e.Metadata.RunID) == "" ||
		strings.TrimSpace(e.Metadata.PluginID) == "" ||
		strings.TrimSpace(e.Metadata.PluginVersion) == "" ||
		strings.TrimSpace(e.Metadata.ProfileID) == "" ||
		strings.TrimSpace(e.Metadata.ConfigVersion) == "" ||
		strings.TrimSpace(e.Metadata.Actor) == "" {
		return fmt.Errorf("job report metadata is incomplete")
	}
	if e.Metadata.Attempt < 1 {
		return fmt.Errorf("job report attempt must be positive")
	}
	for name, value := range map[string]int{
		"total": e.Progress.Total, "processed": e.Progress.Processed,
		"created": e.Progress.Created, "failed": e.Progress.Failed,
		"skipped": e.Progress.Skipped,
	} {
		if value < 0 {
			return fmt.Errorf("job report progress.%s cannot be negative", name)
		}
	}
	if e.Error != nil && strings.TrimSpace(e.Error.Message) == "" {
		return fmt.Errorf("job report error message cannot be empty")
	}
	return nil
}

// JSONLinesReporter emits lines prefixed with a stable marker so cron log
// collectors can distinguish protocol records from human-readable logs.
// It never sends network requests or persists data outside the supplied writer.
type JSONLinesReporter struct {
	writer   io.Writer
	metadata Metadata
	now      func() time.Time
	mu       sync.Mutex
}

func NewJSONLinesReporter(writer io.Writer, metadata Metadata) *JSONLinesReporter {
	return &JSONLinesReporter{writer: writer, metadata: metadata, now: time.Now}
}

func (r *JSONLinesReporter) Emit(kind Kind, status Status, progress Progress, reportError *Error) error {
	event := Event{
		Schema:     Schema,
		Kind:       kind,
		OccurredAt: r.now().UTC().Format(time.RFC3339Nano),
		Metadata:   r.metadata,
		Status:     status,
		Progress:   progress,
		Error:      reportError,
	}
	if err := event.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err = fmt.Fprintf(r.writer, "KERKERKER_JOB_EVENT %s\n", payload)
	return err
}
