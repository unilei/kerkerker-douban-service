// Package jobreport defines the optional progress protocol used by refresh
// workers. One reporter creates an ordered event stream for one or more sinks.
package jobreport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const Schema = "kerkerker.plugin-job.v1"

const legacyExternalReportJobID = "legacy.external-report"

var (
	runIDPattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)
	jobIDPattern          = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,98}[a-z0-9])?$`)
	pluginIDPattern       = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?)*$`)
	rfc3339Pattern        = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$`)
	jobReportTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{32,512}$`)
)

const maxSafeInteger = 1<<53 - 1

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
	JobID         string `json:"job_id"`
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
	EventID    string   `json:"event_id"`
	Sequence   int      `json:"sequence"`
	Kind       Kind     `json:"kind"`
	OccurredAt string   `json:"occurred_at"`
	Metadata   Metadata `json:"metadata"`
	Status     Status   `json:"status"`
	Progress   Progress `json:"progress"`
	Error      *Error   `json:"error,omitempty"`
}

func validateMetadata(metadata Metadata) error {
	if !runIDPattern.MatchString(metadata.RunID) {
		return fmt.Errorf("job report run_id is invalid")
	}
	if !jobIDPattern.MatchString(metadata.JobID) || metadata.JobID == legacyExternalReportJobID {
		return fmt.Errorf("job report job_id is invalid")
	}
	if !pluginIDPattern.MatchString(metadata.PluginID) {
		return fmt.Errorf("job report plugin_id is invalid")
	}
	if utf8.RuneCountInString(metadata.PluginID) > 100 {
		return fmt.Errorf("job report plugin_id is invalid")
	}
	for name, field := range map[string]struct {
		value string
		max   int
	}{
		"plugin_version": {metadata.PluginVersion, 100},
		"profile_id":     {metadata.ProfileID, 100},
		"config_version": {metadata.ConfigVersion, 100},
		"actor":          {metadata.Actor, 200},
	} {
		if err := validateText(name, field.value, field.max); err != nil {
			return err
		}
	}
	if metadata.Attempt < 1 || int64(metadata.Attempt) > maxSafeInteger {
		return fmt.Errorf("job report attempt must be positive")
	}
	return nil
}

func validateText(name, value string, maxLength int) error {
	trimmed := strings.TrimFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || r == '\uFEFF'
	})
	if trimmed == "" || value != trimmed ||
		utf8.RuneCountInString(value) > maxLength ||
		strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("job report %s is invalid", name)
	}
	return nil
}

func validRFC3339Offset(value string) bool {
	if strings.HasSuffix(value, "Z") {
		return true
	}
	if len(value) < 6 {
		return false
	}
	offset := value[len(value)-6:]
	return (offset[0] == '+' || offset[0] == '-') && offset[3] == ':' &&
		offset[1:3] <= "23" && offset[4:6] <= "59"
}

func (e Event) Validate() error {
	if e.Schema != Schema {
		return fmt.Errorf("unsupported job report schema %q", e.Schema)
	}
	if err := validateMetadata(e.Metadata); err != nil {
		return err
	}
	if e.Sequence < 0 || int64(e.Sequence) > maxSafeInteger {
		return fmt.Errorf("job report sequence cannot be negative")
	}
	if e.EventID != fmt.Sprintf("%s:%d", e.Metadata.RunID, e.Sequence) {
		return fmt.Errorf("job report event_id does not match run_id and sequence")
	}
	if len(e.EventID) > 240 {
		return fmt.Errorf("job report event_id is too long")
	}
	if !rfc3339Pattern.MatchString(e.OccurredAt) || !validRFC3339Offset(e.OccurredAt) {
		return fmt.Errorf("occurred_at must be RFC3339")
	}
	if _, err := time.Parse(time.RFC3339Nano, e.OccurredAt); err != nil {
		return fmt.Errorf("occurred_at must be RFC3339: %w", err)
	}
	if e.Kind != KindStarted && e.Kind != KindProgress && e.Kind != KindFinished {
		return fmt.Errorf("invalid job report kind %q", e.Kind)
	}
	if e.Kind == KindStarted && (e.Sequence != 0 || e.Status != StatusRunning) {
		return fmt.Errorf("started job report must use sequence zero and running status")
	}
	if e.Kind != KindStarted && e.Sequence == 0 {
		return fmt.Errorf("sequence zero is reserved for the started event")
	}
	if e.Kind == KindProgress && e.Status != StatusRunning {
		return fmt.Errorf("progress job report must use running status")
	}
	if e.Kind == KindFinished && e.Status != StatusSucceeded && e.Status != StatusPartial && e.Status != StatusFailed {
		return fmt.Errorf("finished job report must use a terminal status")
	}
	if e.Kind != KindFinished && e.Error != nil {
		return fmt.Errorf("only a finished job report can contain an error")
	}
	if e.Kind == KindFinished && e.Status == StatusFailed && e.Error == nil {
		return fmt.Errorf("failed job report must contain an error")
	}
	if e.Kind == KindFinished && e.Status == StatusSucceeded && e.Error != nil {
		return fmt.Errorf("succeeded job report cannot contain an error")
	}
	for name, value := range map[string]int{
		"total": e.Progress.Total, "processed": e.Progress.Processed,
		"created": e.Progress.Created, "failed": e.Progress.Failed,
		"skipped": e.Progress.Skipped,
	} {
		if value < 0 || int64(value) > maxSafeInteger {
			return fmt.Errorf("job report progress.%s cannot be negative", name)
		}
	}
	if e.Progress.Processed > e.Progress.Total {
		return fmt.Errorf("job report progress.processed cannot exceed total")
	}
	if e.Progress.Created+e.Progress.Failed+e.Progress.Skipped != e.Progress.Processed {
		return fmt.Errorf("job report progress categories must equal processed")
	}
	if e.Error != nil {
		if e.Error.Code != "" {
			if err := validateText("error.code", e.Error.Code, 100); err != nil {
				return err
			}
		}
		if err := validateText("error.message", e.Error.Message, 2000); err != nil {
			return err
		}
	}
	return nil
}

// Sink receives one immutable event. Implementations must not mutate it.
type Sink interface {
	WriteEvent(Event) error
}

// Reporter owns the monotonic sequence for a run and sends the exact same
// event to every configured sink while holding the stream order.
type Reporter struct {
	metadata       Metadata
	sinks          []Sink
	now            func() time.Time
	sequence       int
	lastOccurredAt time.Time
	lastProgress   Progress
	pending        []pendingEvent
	terminalQueued bool
	mu             sync.Mutex
}

type pendingEvent struct {
	event     Event
	delivered []bool
}

func validateMonotonicProgress(previous, current Progress) error {
	for _, field := range []struct {
		name     string
		previous int
		current  int
	}{
		{"total", previous.Total, current.Total},
		{"processed", previous.Processed, current.Processed},
		{"created", previous.Created, current.Created},
		{"failed", previous.Failed, current.Failed},
		{"skipped", previous.Skipped, current.Skipped},
	} {
		if field.current < field.previous {
			return fmt.Errorf("job report progress.%s cannot decrease", field.name)
		}
	}
	return nil
}

func NewReporter(metadata Metadata, sinks ...Sink) (*Reporter, error) {
	if err := validateMetadata(metadata); err != nil {
		return nil, err
	}
	if len(sinks) == 0 {
		return nil, fmt.Errorf("job report requires at least one sink")
	}
	for _, sink := range sinks {
		if sink == nil {
			return nil, fmt.Errorf("job report sink cannot be nil")
		}
	}
	return &Reporter{metadata: metadata, sinks: append([]Sink(nil), sinks...), now: time.Now}, nil
}

func (r *Reporter) Emit(kind Kind, status Status, progress Progress, reportError *Error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminalQueued {
		return fmt.Errorf("job report stream is already terminal")
	}
	if r.sequence > 0 {
		if err := validateMonotonicProgress(r.lastProgress, progress); err != nil {
			return err
		}
	}

	occurredAt := r.now().UTC()
	if !r.lastOccurredAt.IsZero() && occurredAt.Before(r.lastOccurredAt) {
		occurredAt = r.lastOccurredAt
	}

	var eventError *Error
	if reportError != nil {
		copy := *reportError
		eventError = &copy
	}
	event := Event{
		Schema:     Schema,
		EventID:    fmt.Sprintf("%s:%d", r.metadata.RunID, r.sequence),
		Sequence:   r.sequence,
		Kind:       kind,
		OccurredAt: occurredAt.Format(time.RFC3339Nano),
		Metadata:   r.metadata,
		Status:     status,
		Progress:   progress,
		Error:      eventError,
	}
	if err := event.Validate(); err != nil {
		return err
	}
	r.lastOccurredAt = occurredAt
	r.lastProgress = progress
	r.sequence++
	r.pending = append(r.pending, pendingEvent{
		event:     event,
		delivered: make([]bool, len(r.sinks)),
	})
	if kind == KindFinished {
		r.terminalQueued = true
	}
	return r.flushLocked()
}

func (r *Reporter) Flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushLocked()
}

func (r *Reporter) flushLocked() error {
	var sinkErrors []error
	for sinkIndex, sink := range r.sinks {
		for eventIndex := range r.pending {
			pending := &r.pending[eventIndex]
			if pending.delivered[sinkIndex] {
				continue
			}
			if err := sink.WriteEvent(pending.event); err != nil {
				sinkErrors = append(sinkErrors, err)
				break
			}
			pending.delivered[sinkIndex] = true
		}
	}
	for len(r.pending) > 0 {
		allDelivered := true
		for _, delivered := range r.pending[0].delivered {
			if !delivered {
				allDelivered = false
				break
			}
		}
		if !allDelivered {
			break
		}
		r.pending = r.pending[1:]
	}
	return errors.Join(sinkErrors...)
}

// JSONLinesSink emits a stable marker followed by one JSON event per line.
type JSONLinesSink struct {
	writer io.Writer
	mu     sync.Mutex
}

func NewJSONLinesSink(writer io.Writer) *JSONLinesSink {
	return &JSONLinesSink{writer: writer}
}

func (s *JSONLinesSink) WriteEvent(event Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = fmt.Fprintf(s.writer, "KERKERKER_JOB_EVENT %s\n", payload)
	return err
}

func NewJSONLinesReporter(writer io.Writer, metadata Metadata) (*Reporter, error) {
	return NewReporter(metadata, NewJSONLinesSink(writer))
}

// HTTPSink posts events to the host job-report endpoint. Redirects are never
// followed so the bearer token cannot be forwarded to another destination.
type HTTPSink struct {
	endpoint   string
	token      string
	client     *http.Client
	retryDelay func(int)
}

func NewHTTPSink(endpoint, token string, client *http.Client) (*HTTPSink, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("job report endpoint is invalid")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return nil, fmt.Errorf("job report endpoint must use HTTPS")
	}
	if !jobReportTokenPattern.MatchString(token) {
		return nil, fmt.Errorf("job report token is invalid")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	clientCopy := *client
	if clientCopy.Timeout == 0 {
		clientCopy.Timeout = 10 * time.Second
	}
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &HTTPSink{
		endpoint: parsed.String(),
		token:    token,
		client:   &clientCopy,
		retryDelay: func(attempt int) {
			time.Sleep(time.Duration(1<<attempt) * 250 * time.Millisecond)
		},
	}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func retryableStatus(status int) bool {
	return status == http.StatusConflict || status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func (s *HTTPSink) WriteEvent(event Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	const attempts = 3
	lastStatus := 0
	for attempt := 0; attempt < attempts; attempt++ {
		request, requestErr := http.NewRequest(http.MethodPost, s.endpoint, bytes.NewReader(payload))
		if requestErr != nil {
			return fmt.Errorf("job report request could not be created")
		}
		request.Header.Set("Authorization", "Bearer "+s.token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "kerkerker-douban-refresh/1")

		response, requestErr := s.client.Do(request)
		if requestErr == nil {
			lastStatus = response.StatusCode
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4*1024))
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
			if !retryableStatus(response.StatusCode) {
				return fmt.Errorf("job report endpoint returned HTTP %d", response.StatusCode)
			}
		}
		if attempt+1 < attempts {
			s.retryDelay(attempt)
		}
	}
	if lastStatus != 0 {
		return fmt.Errorf("job report endpoint remained unavailable with HTTP %d", lastStatus)
	}
	return fmt.Errorf("job report endpoint remained unreachable")
}
