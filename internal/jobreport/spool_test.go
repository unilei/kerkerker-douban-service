package jobreport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestDurableHTTPSinkRetainsAndReplaysFailedEvents(t *testing.T) {
	var requests atomic.Int32
	var accept atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if !accept.Load() {
			http.Error(writer, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	sink, err := NewDurableHTTPSink(server.URL, testJobReportToken, path, server.Client(), 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{
		Schema: Schema, EventID: "refresh-1:0", Sequence: 0, Kind: KindStarted,
		OccurredAt: "2026-08-23T00:00:00Z", Metadata: testMetadata(), Status: StatusRunning,
	}
	if err := sink.WriteEvent(event); err == nil {
		t.Fatal("expected failed delivery")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected event spool: %v", err)
	}

	accept.Store(true)
	restarted, err := NewDurableHTTPSink(server.URL, testJobReportToken, path, server.Client(), 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Replay(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected spool to be drained, got %v", err)
	}
	if requests.Load() < 2 {
		t.Fatalf("expected initial delivery and replay, got %d", requests.Load())
	}
}

func TestDurableHTTPSinkDoesNotDuplicateQueuedEventOnRetry(t *testing.T) {
	var accept atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !accept.Load() {
			http.Error(writer, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	sink, err := NewDurableHTTPSink(server.URL, testJobReportToken, path, server.Client(), 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{
		Schema: Schema, EventID: "refresh-1:0", Sequence: 0, Kind: KindStarted,
		OccurredAt: "2026-08-23T00:00:00Z", Metadata: testMetadata(), Status: StatusRunning,
	}
	_ = sink.WriteEvent(event)
	_ = sink.WriteEvent(event)
	accept.Store(true)
	if err := sink.Replay(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected one queued event to be removed, got %v", err)
	}
}

func TestDurableHTTPSinkRejectsOversizedSpool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	sink, err := NewDurableHTTPSink("http://127.0.0.1:1", testJobReportToken, path, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{
		Schema: Schema, EventID: "refresh-1:0", Sequence: 0, Kind: KindStarted,
		OccurredAt: "2026-08-23T00:00:00Z", Metadata: testMetadata(), Status: StatusRunning,
	}
	if err := sink.WriteEvent(event); err == nil {
		t.Fatal("expected spool full error")
	}
	if payload, readErr := os.ReadFile(path); readErr == nil {
		var decoded Event
		if json.Unmarshal(payload, &decoded) == nil {
			t.Fatal("oversized spool must not contain a complete event")
		}
	}
}
