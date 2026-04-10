// Package simrun tracks per-simulation run lifecycle (start, progress, stop).
package simrun

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PlatformStats tracks per-platform progress during a run.
type PlatformStats struct {
	Posts   int `json:"posts"`
	Actions int `json:"actions"`
}

// EnvHealth captures the last known health of the runtime environment.
type EnvHealth struct {
	LLMReachable bool   `json:"llm_reachable"`
	DBWritable   bool   `json:"db_writable"`
	CheckedAt    string `json:"checked_at"` // RFC3339
}

// SimRun tracks one execution of fishnet sim platform.
type SimRun struct {
	ID        string        `json:"id"`         // "run-20240409-143022"
	SessionID string        `json:"session_id"` // links to a session if prepared
	ProjectID string        `json:"project_id"`
	Scenario  string        `json:"scenario"`
	Status    string        `json:"status"`     // pending|running|completed|failed|stopped
	PID       int           `json:"pid"`
	Rounds    int           `json:"rounds"`     // rounds completed so far
	MaxRounds int           `json:"max_rounds"`
	Platforms []string      `json:"platforms"`
	Twitter   PlatformStats `json:"twitter"`
	Reddit    PlatformStats `json:"reddit"`
	Health    *EnvHealth    `json:"health,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt time.Time    `json:"finished_at"`
	ErrorMsg  string        `json:"error_msg"`
}

// Progress returns 0-100 percent of rounds complete.
func (r *SimRun) Progress() int {
	if r.MaxRounds <= 0 {
		return 0
	}
	pct := r.Rounds * 100 / r.MaxRounds
	if pct > 100 {
		return 100
	}
	return pct
}

func (r *SimRun) MarkRunning(pid int) {
	r.Status = "running"
	r.PID = pid
	r.StartedAt = time.Now()
	r.ErrorMsg = ""
}

func (r *SimRun) MarkCompleted() {
	r.Status = "completed"
	r.FinishedAt = time.Now()
	r.Rounds = r.MaxRounds
}

func (r *SimRun) MarkFailed(msg string) {
	r.Status = "failed"
	r.FinishedAt = time.Now()
	r.ErrorMsg = msg
}

func (r *SimRun) MarkStopped() {
	r.Status = "stopped"
	r.FinishedAt = time.Now()
}

// Manager persists SimRun objects at .fishnet/simruns/.
type Manager struct {
	dir string
}

func NewManager(workDir string) *Manager {
	return &Manager{dir: filepath.Join(workDir, ".fishnet", "simruns")}
}

func (m *Manager) ensureDir() error {
	return os.MkdirAll(m.dir, 0755)
}

func (m *Manager) path(id string) string {
	return filepath.Join(m.dir, id+".json")
}

// NewRunID generates a time-based run ID like "run-20240409-143022".
func NewRunID() string {
	return time.Now().Format("run-20060102-150405")
}

// Create creates and persists a new SimRun.
func (m *Manager) Create(projectID, sessionID, scenario string, maxRounds int, platforms []string) (*SimRun, error) {
	if err := m.ensureDir(); err != nil {
		return nil, err
	}
	r := &SimRun{
		ID:        NewRunID(),
		SessionID: sessionID,
		ProjectID: projectID,
		Scenario:  scenario,
		Status:    "pending",
		MaxRounds: maxRounds,
		Platforms: platforms,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return r, m.Save(r)
}

// Save persists the SimRun to disk.
func (m *Manager) Save(r *SimRun) error {
	if err := m.ensureDir(); err != nil {
		return err
	}
	r.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path(r.ID), data, 0644)
}

// Load retrieves a SimRun by exact ID or prefix match.
func (m *Manager) Load(ref string) (*SimRun, error) {
	if data, err := os.ReadFile(m.path(ref)); err == nil {
		var r SimRun
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, err
		}
		return &r, nil
	}
	all, err := m.List()
	if err != nil {
		return nil, err
	}
	refLow := strings.ToLower(ref)
	for _, r := range all {
		if strings.HasPrefix(strings.ToLower(r.ID), refLow) {
			return r, nil
		}
	}
	return nil, fmt.Errorf("run %q not found", ref)
}

// List returns all SimRuns sorted by CreatedAt descending.
func (m *Manager) List() ([]*SimRun, error) {
	if err := m.ensureDir(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*SimRun
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			continue
		}
		var r SimRun
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		out = append(out, &r)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
