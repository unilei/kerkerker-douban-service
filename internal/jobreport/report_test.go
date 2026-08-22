package jobreport

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const testJobReportToken = "test-report-token-0123456789abcdef"

type flakySink struct {
	failuresRemaining int
	attempts          []Event
	delivered         []Event
}

func (s *flakySink) WriteEvent(event Event) error {
	s.attempts = append(s.attempts, event)
	if s.failuresRemaining > 0 {
		s.failuresRemaining--
		return errors.New("temporary sink failure")
	}
	s.delivered = append(s.delivered, event)
	return nil
}

func testMetadata() Metadata {
	return Metadata{
		RunID: "refresh-1", PluginID: "kerkerker.douban-content", PluginVersion: "1.0.0",
		ProfileID: "cn-default", ConfigVersion: "runtime", Actor: "system/refresh", Attempt: 1,
	}
}

func TestJSONLinesReporterEmitsOrderedMachineReadableEvents(t *testing.T) {
	var output bytes.Buffer
	reporter, err := NewJSONLinesReporter(&output, testMetadata())
	if err != nil {
		t.Fatal(err)
	}
	reporter.now = func() time.Time { return time.Date(2026, 8, 21, 0, 0, 0, 123, time.UTC) }
	if err := reporter.Emit(KindStarted, StatusRunning, Progress{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := reporter.Emit(KindProgress, StatusRunning, Progress{Total: 10, Processed: 4, Created: 3, Failed: 1}, nil); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two protocol lines, got %d", len(lines))
	}
	for sequence, line := range lines {
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
		if event.Sequence != sequence || event.EventID != "refresh-1:"+strconv.Itoa(sequence) {
			t.Fatalf("unexpected event ordering: %+v", event)
		}
	}
}

func TestEventValidationRejectsBrokenIdentityStateAndProgress(t *testing.T) {
	event := Event{
		Schema: Schema, EventID: "refresh-1:0", Sequence: 0, Kind: KindStarted,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano), Metadata: testMetadata(),
		Status: StatusRunning, Progress: Progress{},
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	event.EventID = "other"
	if err := event.Validate(); err == nil {
		t.Fatal("expected mismatched event_id to be rejected")
	}
	event.EventID = "refresh-1:0"
	event.Progress = Progress{Total: 2, Processed: 1}
	if err := event.Validate(); err == nil {
		t.Fatal("expected uncategorized progress to be rejected")
	}
	event.Progress = Progress{}
	event.Kind = KindFinished
	event.Status = StatusFailed
	if err := event.Validate(); err == nil {
		t.Fatal("expected a sequence-zero finished event to be rejected")
	}
	for _, invalidTimestamp := range []string{
		"2026-02-30T00:00:00Z",
		"2026-02-28T24:00:00Z",
		"2026-02-28T23:60:00Z",
		"2026-02-28T23:59:60Z",
		"2026-02-28T23:59:59+24:00",
	} {
		event := Event{
			Schema: Schema, EventID: "refresh-1:0", Sequence: 0, Kind: KindStarted,
			OccurredAt: invalidTimestamp, Metadata: testMetadata(), Status: StatusRunning,
		}
		if err := event.Validate(); err == nil {
			t.Fatalf("expected invalid timestamp %q to be rejected", invalidTimestamp)
		}
	}
}

func TestGoldenFixtureMatchesThePublicJobEventContract(t *testing.T) {
	payload, err := os.ReadFile("testdata/plugin-job-event.v1.valid.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var event Event
	if err := decoder.Decode(&event); err != nil {
		t.Fatal(err)
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	if event.EventID != event.Metadata.RunID+":"+strconv.Itoa(event.Sequence) {
		t.Fatalf("golden fixture has inconsistent identity: %+v", event)
	}
}

func TestSharedInvalidFixtureIsRejected(t *testing.T) {
	payload, err := os.ReadFile("testdata/plugin-job-event.v1.invalid.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var event Event
	if err := decoder.Decode(&event); err != nil {
		t.Fatal(err)
	}
	if err := event.Validate(); err == nil {
		t.Fatal("expected shared invalid fixture to be rejected")
	}
}

func TestReporterReplaysEachSinkInOrderAndClampsClockRollback(t *testing.T) {
	stable := &flakySink{}
	catchingUp := &flakySink{failuresRemaining: 2}
	reporter, err := NewReporter(testMetadata(), stable, catchingUp)
	if err != nil {
		t.Fatal(err)
	}
	times := []time.Time{
		time.Date(2026, 8, 22, 7, 0, 1, 0, time.UTC),
		time.Date(2026, 8, 22, 7, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 22, 7, 0, 2, 0, time.UTC),
	}
	reporter.now = func() time.Time {
		value := times[0]
		times = times[1:]
		return value
	}

	if err := reporter.Emit(KindStarted, StatusRunning, Progress{Total: 1}, nil); err == nil {
		t.Fatal("expected the first sink failure to be reported")
	}
	if err := reporter.Emit(KindProgress, StatusRunning, Progress{Total: 1}, nil); err == nil {
		t.Fatal("expected the second sink failure to be reported")
	}
	if len(stable.delivered) != 2 || stable.delivered[0].Sequence != 0 || stable.delivered[1].Sequence != 1 {
		t.Fatalf("stable sink did not advance independently in order: %+v", stable.delivered)
	}
	if len(stable.attempts) != 2 {
		t.Fatalf("stable sink received duplicate retries: %+v", stable.attempts)
	}
	if stable.delivered[1].OccurredAt != stable.delivered[0].OccurredAt {
		t.Fatalf("clock rollback was not clamped: %+v", stable.delivered)
	}

	if err := reporter.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(catchingUp.delivered) != 2 || catchingUp.delivered[0].Sequence != 0 || catchingUp.delivered[1].Sequence != 1 {
		t.Fatalf("recovering sink did not replay in order: %+v", catchingUp.delivered)
	}
	if err := reporter.Emit(KindFinished, StatusSucceeded, Progress{Total: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if err := reporter.Emit(KindProgress, StatusRunning, Progress{Total: 1}, nil); err == nil {
		t.Fatal("expected events after the terminal event to be rejected")
	}
}

func TestReporterRejectsQueuedProgressRegressionWithoutConsumingSequence(t *testing.T) {
	sink := &flakySink{failuresRemaining: 1}
	reporter, err := NewReporter(testMetadata(), sink)
	if err != nil {
		t.Fatal(err)
	}
	initial := Progress{Total: 10, Processed: 4, Created: 2, Failed: 1, Skipped: 1}
	if err := reporter.Emit(KindStarted, StatusRunning, initial, nil); err == nil {
		t.Fatal("expected initial sink failure")
	}
	if len(sink.attempts) != 1 {
		t.Fatalf("expected one attempted event, got %d", len(sink.attempts))
	}
	regressed := Progress{Total: 10, Processed: 3, Created: 2, Failed: 1}
	if err := reporter.Emit(KindProgress, StatusRunning, regressed, nil); err == nil {
		t.Fatal("expected queued progress regression to be rejected")
	}
	if len(sink.attempts) != 1 {
		t.Fatalf("rejected event reached the sink: %+v", sink.attempts)
	}

	valid := Progress{Total: 11, Processed: 5, Created: 3, Failed: 1, Skipped: 1}
	if err := reporter.Emit(KindProgress, StatusRunning, valid, nil); err != nil {
		t.Fatal(err)
	}
	if len(sink.delivered) != 2 || sink.delivered[0].Sequence != 0 || sink.delivered[1].Sequence != 1 {
		t.Fatalf("rejected progress consumed a sequence or blocked recovery: %+v", sink.delivered)
	}
}

func TestMonotonicProgressChecksEveryCumulativeField(t *testing.T) {
	previous := Progress{Total: 10, Processed: 4, Created: 2, Failed: 1, Skipped: 1}
	for name, current := range map[string]Progress{
		"total":     {Total: 9, Processed: 4, Created: 2, Failed: 1, Skipped: 1},
		"processed": {Total: 10, Processed: 3, Created: 2, Failed: 1},
		"created":   {Total: 10, Processed: 4, Created: 1, Failed: 2, Skipped: 1},
		"failed":    {Total: 10, Processed: 4, Created: 3, Failed: 0, Skipped: 1},
		"skipped":   {Total: 10, Processed: 4, Created: 3, Failed: 1, Skipped: 0},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateMonotonicProgress(previous, current); err == nil {
				t.Fatalf("expected %s regression to be rejected", name)
			}
		})
	}
}

func TestReporterCopiesErrorsBeforeQueuing(t *testing.T) {
	sink := &flakySink{}
	reporter, err := NewReporter(testMetadata(), sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := reporter.Emit(KindStarted, StatusRunning, Progress{}, nil); err != nil {
		t.Fatal(err)
	}
	sink.failuresRemaining = 1
	reportError := &Error{Code: "CONFIG", Message: "original"}
	if err := reporter.Emit(KindFinished, StatusFailed, Progress{}, reportError); err == nil {
		t.Fatal("expected the sink failure to be reported")
	}
	reportError.Message = "mutated"
	if err := reporter.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(sink.delivered) != 2 || sink.delivered[1].Error == nil {
		t.Fatalf("expected queued terminal event to be delivered: %+v", sink.delivered)
	}
	if got := sink.delivered[1].Error.Message; got != "original" {
		t.Fatalf("queued error was mutated through the caller pointer: %q", got)
	}
}

func TestReporterFlushesQueuedTerminalPerSinkWithoutDuplicates(t *testing.T) {
	healthy := &flakySink{}
	recovering := &flakySink{}
	reporter, err := NewReporter(testMetadata(), healthy, recovering)
	if err != nil {
		t.Fatal(err)
	}
	if err := reporter.Emit(KindStarted, StatusRunning, Progress{}, nil); err != nil {
		t.Fatal(err)
	}
	recovering.failuresRemaining = 1
	if err := reporter.Emit(KindFinished, StatusSucceeded, Progress{}, nil); err == nil {
		t.Fatal("expected terminal delivery failure to be reported")
	}
	if len(healthy.delivered) != 2 || len(healthy.attempts) != 2 {
		t.Fatalf("healthy sink received a duplicate or missed terminal event: %+v", healthy.attempts)
	}
	if len(recovering.delivered) != 1 {
		t.Fatalf("recovering sink advanced past its failed terminal event: %+v", recovering.delivered)
	}
	if err := reporter.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(healthy.attempts) != 2 {
		t.Fatalf("healthy sink was retried after acknowledging terminal: %+v", healthy.attempts)
	}
	if len(recovering.delivered) != 2 || recovering.delivered[1].Kind != KindFinished {
		t.Fatalf("recovering sink did not receive queued terminal event: %+v", recovering.delivered)
	}
	if err := reporter.Emit(KindProgress, StatusRunning, Progress{}, nil); err == nil {
		t.Fatal("expected queued terminal event to seal the stream")
	}
}

func TestHTTPSinkRetriesTheSameEventAndKeepsBearerPrivate(t *testing.T) {
	var mu sync.Mutex
	var events []Event
	var authorizations []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var event Event
		if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
			t.Errorf("decode event: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		events = append(events, event)
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		attempt := len(events)
		mu.Unlock()
		if attempt < 3 {
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink, err := NewHTTPSink(server.URL, testJobReportToken, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	sink.retryDelay = func(int) {}
	reporter, err := NewReporter(testMetadata(), sink)
	if err != nil {
		t.Fatal(err)
	}
	if err := reporter.Emit(KindStarted, StatusRunning, Progress{}, nil); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 3 {
		t.Fatalf("expected three attempts, got %d", len(events))
	}
	for index, event := range events {
		if event.EventID != "refresh-1:0" || event.Sequence != 0 {
			t.Fatalf("retry %d changed event identity: %+v", index, event)
		}
		if authorizations[index] != "Bearer "+testJobReportToken {
			t.Fatalf("retry %d omitted authorization", index)
		}
	}
}

func TestHTTPSinkRejectsInsecureRemoteEndpointsAndCredentials(t *testing.T) {
	if _, err := NewHTTPSink("http://example.com/api/plugins/jobs/report", testJobReportToken, nil); err == nil {
		t.Fatal("expected remote HTTP endpoint to be rejected")
	}
	if _, err := NewHTTPSink("https://user:password@example.com/report", testJobReportToken, nil); err == nil {
		t.Fatal("expected URL credentials to be rejected")
	}
	if _, err := NewHTTPSink("http://127.0.0.1:3000/report", testJobReportToken, nil); err != nil {
		t.Fatalf("expected loopback HTTP endpoint to be accepted: %v", err)
	}
	if _, err := NewHTTPSink("https://example.com/report", " "+testJobReportToken, nil); err == nil {
		t.Fatal("expected a token with surrounding whitespace to be rejected")
	}
	if _, err := NewHTTPSink("https://example.com/report", "short-secret", nil); err == nil {
		t.Fatal("expected a short token to be rejected")
	}
}

func TestHTTPSinkNeverFollowsRedirectsOrForwardsBearer(t *testing.T) {
	var targetAuthorization string
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/target" {
			targetAuthorization = request.Header.Get("Authorization")
			response.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(response, request, "/target", http.StatusFound)
	}))
	defer server.Close()

	sink, err := NewHTTPSink(server.URL+"/redirect", testJobReportToken, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	event := Event{
		Schema: Schema, EventID: "refresh-1:0", Sequence: 0, Kind: KindStarted,
		OccurredAt: time.Now().UTC().Format(time.RFC3339Nano), Metadata: testMetadata(),
		Status: StatusRunning, Progress: Progress{},
	}
	if err := sink.WriteEvent(event); err == nil {
		t.Fatal("expected redirect response to be rejected")
	}
	if targetAuthorization != "" {
		t.Fatalf("bearer token reached redirect target: %q", targetAuthorization)
	}
}
