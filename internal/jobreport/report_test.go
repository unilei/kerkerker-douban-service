package jobreport

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestJSONLinesReporterEmitsValidatedMachineReadableEvent(t *testing.T) {
	var output bytes.Buffer
	reporter := NewJSONLinesReporter(&output, Metadata{
		RunID: "refresh-1", PluginID: "kerkerker.douban-content", PluginVersion: "runtime",
		ProfileID: "cn-default", ConfigVersion: "runtime", Actor: "system/refresh", Attempt: 1,
	})
	reporter.now = func() time.Time { return time.Date(2026, 8, 21, 0, 0, 0, 123, time.UTC) }
	if err := reporter.Emit(KindProgress, StatusRunning, Progress{Total: 10, Processed: 4, Created: 3, Failed: 1}, nil); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(output.String())
	if !strings.HasPrefix(line, "KERKERKER_JOB_EVENT ") {
		t.Fatalf("missing protocol marker: %q", line)
	}
	var event Event
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "KERKERKER_JOB_EVENT ")), &event); err != nil {
		t.Fatal(err)
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	if event.Metadata.RunID != "refresh-1" || event.Progress.Processed != 4 {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestEventValidationRejectsIncompleteMetadataAndNegativeProgress(t *testing.T) {
	event := Event{
		Schema: Schema, Kind: KindProgress, OccurredAt: time.Now().UTC().Format(time.RFC3339Nano),
		Metadata: Metadata{RunID: "run", PluginID: "plugin", PluginVersion: "v1", ProfileID: "cn", ConfigVersion: "v1", Actor: "system", Attempt: 1},
		Status:   StatusRunning, Progress: Progress{Processed: -1},
	}
	if err := event.Validate(); err == nil {
		t.Fatal("expected negative progress to be rejected")
	}
	event.Progress.Processed = 0
	event.Metadata.RunID = ""
	if err := event.Validate(); err == nil {
		t.Fatal("expected incomplete metadata to be rejected")
	}
}
