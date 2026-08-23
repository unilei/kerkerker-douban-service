package jobreport

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const defaultSpoolMaxBytes int64 = 64 * 1024 * 1024

// DurableHTTPSink keeps one JSON event per line until the host acknowledges it.
// The file is deliberately local to the worker and contains no bearer token.
type DurableHTTPSink struct {
	remote  *HTTPSink
	path    string
	maxSize int64
	mu      sync.Mutex
}

func NewDurableHTTPSink(endpoint, token, path string, client *http.Client, maxSize int64) (*DurableHTTPSink, error) {
	remote, err := NewHTTPSink(endpoint, token, client)
	if err != nil {
		return nil, err
	}
	path = strings.TrimSpace(path)
	if path == "" || strings.ContainsAny(path, "\x00\r\n") {
		return nil, fmt.Errorf("job report spool path is invalid")
	}
	if maxSize <= 0 {
		maxSize = defaultSpoolMaxBytes
	}
	if maxSize > 1<<30 {
		return nil, fmt.Errorf("job report spool max size is too large")
	}
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > maxSize {
		return nil, fmt.Errorf("job report spool exceeds configured max size")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("job report spool cannot be inspected")
	}
	return &DurableHTTPSink{remote: remote, path: path, maxSize: maxSize}, nil
}

// Replay drains acknowledged events from a previous process. A transient
// host failure stops the replay and leaves the remaining lines intact.
func (s *DurableHTTPSink) Replay() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replayLocked()
}

func (s *DurableHTTPSink) WriteEvent(event Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.appendLocked(event); err != nil {
		return err
	}
	if err := s.remote.WriteEvent(event); err != nil {
		return err
	}
	return s.removeLocked(event.EventID)
}

func (s *DurableHTTPSink) appendLocked(event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("job report spool event could not be encoded")
	}
	line := append(payload, '\n')
	if existing, readErr := readSpoolLines(s.path); readErr != nil {
		return readErr
	} else {
		for _, line := range existing {
			var queued Event
			if json.Unmarshal(line, &queued) == nil && queued.EventID == event.EventID {
				return nil
			}
		}
	}
	info, statErr := os.Stat(s.path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("job report spool cannot be inspected")
	}
	currentSize := int64(0)
	if info != nil {
		currentSize = info.Size()
	}
	if currentSize+int64(len(line)) > s.maxSize {
		return fmt.Errorf("job report spool is full")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("job report spool directory could not be created")
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("job report spool could not be opened")
	}
	defer file.Close()
	if _, err := file.Write(line); err != nil {
		return fmt.Errorf("job report spool could not be written")
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("job report spool could not be synced")
	}
	return nil
}

func (s *DurableHTTPSink) replayLocked() error {
	lines, err := readSpoolLines(s.path)
	if err != nil {
		return err
	}
	for len(lines) > 0 {
		var event Event
		if err := json.Unmarshal(lines[0], &event); err != nil {
			return fmt.Errorf("job report spool contains invalid JSON")
		}
		if err := event.Validate(); err != nil {
			return fmt.Errorf("job report spool contains invalid event")
		}
		if err := s.remote.WriteEvent(event); err != nil {
			return err
		}
		lines = lines[1:]
		if err := writeSpoolLines(s.path, lines); err != nil {
			return err
		}
	}
	return nil
}

func (s *DurableHTTPSink) removeLocked(eventID string) error {
	lines, err := readSpoolLines(s.path)
	if err != nil {
		return err
	}
	for index, line := range lines {
		var event Event
		if json.Unmarshal(line, &event) == nil && event.EventID == eventID {
			lines = append(lines[:index], lines[index+1:]...)
			break
		}
	}
	return writeSpoolLines(s.path, lines)
}

func readSpoolLines(path string) ([][]byte, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("job report spool could not be opened")
	}
	defer file.Close()
	var lines [][]byte
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 4*1024), 256*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("job report spool could not be read")
	}
	return lines, nil
}

func writeSpoolLines(path string, lines [][]byte) error {
	if len(lines) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("job report spool could not be removed")
		}
		return nil
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".job-report-spool-*")
	if err != nil {
		return fmt.Errorf("job report spool temporary file could not be created")
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("job report spool permissions could not be set")
	}
	for _, line := range lines {
		if _, err := temporary.Write(append(line, '\n')); err != nil {
			_ = temporary.Close()
			return fmt.Errorf("job report spool could not be rewritten")
		}
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("job report spool could not be synced")
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("job report spool could not be closed")
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("job report spool could not be replaced")
	}
	return nil
}
