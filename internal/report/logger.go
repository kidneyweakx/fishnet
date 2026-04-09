package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// LogEntry records one step in the agent's reasoning process.
type LogEntry struct {
	Time    string `json:"time"`
	Stage   string `json:"stage"`             // "planning"|"tool_call"|"tool_result"|"section_content"|"complete"
	Section string `json:"section,omitempty"` // which report section
	Tool    string `json:"tool,omitempty"`    // tool name when stage is tool_call/tool_result
	Query   string `json:"query,omitempty"`   // query or topic passed to the tool
	Content string `json:"content"`           // human-readable description or content snippet
}

// Logger writes agent reasoning steps to a JSONL file.
type Logger struct {
	reportID string
	file     *os.File
	mu       sync.Mutex
}

// NewLogger creates a Logger that appends JSONL entries to
// <dir>/<reportID>.jsonl, creating <dir> if needed.
// Returns (nil, err) if the file cannot be opened — callers should handle nil.
func NewLogger(dir, reportID string) (*Logger, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("report logger: create dir %s: %w", dir, err)
	}
	path := filepath.Join(dir, reportID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("report logger: open %s: %w", path, err)
	}
	return &Logger{reportID: reportID, file: f}, nil
}

// Log appends entry to the JSONL file. Safe for concurrent use.
// Silently drops the entry if the file is closed or an error occurs.
func (l *Logger) Log(entry LogEntry) {
	if l == nil {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.file.Write(append(data, '\n'))
}

// Close flushes and closes the underlying file.
func (l *Logger) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.file.Close()
}
