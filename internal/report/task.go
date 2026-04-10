package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SectionRecord tracks one section of a report generation job.
type SectionRecord struct {
	Index     int    `json:"index"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
}

// Task tracks the lifecycle of a report generation job.
type Task struct {
	ID            string          `json:"id"`
	Scenario      string          `json:"scenario"`
	ProjectID     string          `json:"project_id"`
	Status        string          `json:"status"` // pending|running|completed|failed
	Sections      []SectionRecord `json:"sections"`
	SectionsDone  int             `json:"sections_done"`
	SectionsTotal int             `json:"sections_total"`
	ReportFile    string          `json:"report_file"`
	LogFile       string          `json:"log_file"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    time.Time       `json:"finished_at"`
	ErrorMsg      string          `json:"error_msg"`
}

// Progress returns 0-100 percent of sections complete.
func (t *Task) Progress() int {
	if t.SectionsTotal <= 0 {
		return 0
	}
	return t.SectionsDone * 100 / t.SectionsTotal
}

func (t *Task) MarkRunning() {
	t.Status = "running"
	t.StartedAt = time.Now()
}

func (t *Task) MarkCompleted() {
	t.Status = "completed"
	t.FinishedAt = time.Now()
}

func (t *Task) MarkFailed(msg string) {
	t.Status = "failed"
	t.FinishedAt = time.Now()
	t.ErrorMsg = msg
}

// TaskManager persists Task objects alongside reports in .fishnet/reports/.
type TaskManager struct {
	dir string
}

// NewTaskManager creates a TaskManager rooted at the working directory.
func NewTaskManager(workDir string) *TaskManager {
	return &TaskManager{dir: filepath.Join(workDir, ".fishnet", "reports")}
}

func (m *TaskManager) path(id string) string {
	return filepath.Join(m.dir, id+".task.json")
}

// Save persists the task.
func (m *TaskManager) Save(t *Task) error {
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return err
	}
	t.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path(t.ID), data, 0644)
}

// Load retrieves a task by report ID.
func (m *TaskManager) Load(id string) (*Task, error) {
	data, err := os.ReadFile(m.path(id))
	if err != nil {
		return nil, fmt.Errorf("report task %q not found", id)
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// ReadLogEntries reads all JSONL log entries for a report ID.
func ReadLogEntries(workDir, reportID string) ([]LogEntry, error) {
	path := filepath.Join(workDir, ".fishnet", "reports", reportID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("log file not found for report %q", reportID)
	}
	defer f.Close()

	var entries []LogEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e LogEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}
