package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Session stores a named simulation configuration snapshot.
type Session struct {
	ID        string    `json:"id"`        // short human-readable ID e.g. "sim-20240409-143022"
	Name      string    `json:"name"`      // optional user-given name
	Scenario  string    `json:"scenario"`
	Platforms []string  `json:"platforms"` // ["twitter","reddit"]
	Rounds    int       `json:"rounds"`
	MaxAgents int       `json:"max_agents"`
	TimeZone  string    `json:"timezone"`
	SimID     string    `json:"sim_id"`    // DB sim_id after running (empty if not run yet)
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Tags      []string  `json:"tags"`  // free-form tags for filtering
	Notes     string    `json:"notes"` // user notes

	// Runtime state fields (mirrors MiroFish SimulationState / SimulationRunState)
	Status     string    `json:"status"`      // created|running|paused|completed|failed
	StartedAt  time.Time `json:"started_at"`  // zero if not yet started
	FinishedAt time.Time `json:"finished_at"` // zero if not yet finished
	ErrorMsg   string    `json:"error_msg"`   // non-empty when Status == "failed"
	Progress   int       `json:"progress"`    // 0-100 percent of rounds completed
}

// MarkRunning transitions the session to the "running" state.
func (s *Session) MarkRunning() {
	s.Status = "running"
	s.StartedAt = time.Now()
	s.ErrorMsg = ""
}

// MarkPaused transitions the session to the "paused" state.
func (s *Session) MarkPaused() {
	s.Status = "paused"
}

// MarkResumed transitions the session back to "running" from "paused".
func (s *Session) MarkResumed() {
	s.Status = "running"
}

// MarkCompleted transitions the session to the "completed" state.
func (s *Session) MarkCompleted() {
	s.Status = "completed"
	s.FinishedAt = time.Now()
	s.Progress = 100
}

// MarkFailed transitions the session to the "failed" state with an error message.
func (s *Session) MarkFailed(errMsg string) {
	s.Status = "failed"
	s.FinishedAt = time.Now()
	s.ErrorMsg = errMsg
}

// SetProgress updates the 0-100 progress percentage.
func (s *Session) SetProgress(round, maxRounds int) {
	if maxRounds <= 0 {
		return
	}
	pct := round * 100 / maxRounds
	if pct > 100 {
		pct = 100
	}
	s.Progress = pct
}

// Manager handles session persistence.
// Sessions are stored in .fishnet/sessions/ as JSON files.
type Manager struct {
	dir string // .fishnet/sessions/
}

// NewManager creates a Manager rooted at projectDir.
func NewManager(projectDir string) *Manager {
	return &Manager{dir: filepath.Join(projectDir, ".fishnet", "sessions")}
}

func (m *Manager) ensureDir() error {
	return os.MkdirAll(m.dir, 0755)
}

func (m *Manager) path(id string) string {
	return filepath.Join(m.dir, id+".json")
}

// Save persists a session (create or update by ID).
func (m *Manager) Save(s *Session) error {
	if err := m.ensureDir(); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}
	s.UpdatedAt = time.Now()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = s.UpdatedAt
	}
	if s.ID == "" {
		s.ID = NewID()
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path(s.ID), data, 0644)
}

// Load retrieves a session by ID or name (case-insensitive prefix match).
func (m *Manager) Load(ref string) (*Session, error) {
	// Try direct ID lookup first.
	p := m.path(ref)
	if data, err := os.ReadFile(p); err == nil {
		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, err
		}
		return &s, nil
	}

	// Fall back to scanning all sessions for name/prefix match.
	all, err := m.List()
	if err != nil {
		return nil, err
	}
	refLow := strings.ToLower(ref)
	for _, s := range all {
		if strings.ToLower(s.ID) == refLow ||
			strings.HasPrefix(strings.ToLower(s.ID), refLow) ||
			strings.ToLower(s.Name) == refLow ||
			strings.HasPrefix(strings.ToLower(s.Name), refLow) {
			return s, nil
		}
	}
	return nil, fmt.Errorf("session %q not found", ref)
}

// List returns all sessions sorted by UpdatedAt descending.
func (m *Manager) List() ([]*Session, error) {
	if err := m.ensureDir(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		out = append(out, &s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// Delete removes a session by ID.
func (m *Manager) Delete(id string) error {
	// Resolve by ref in case it's a name.
	s, err := m.Load(id)
	if err != nil {
		return err
	}
	return os.Remove(m.path(s.ID))
}

// NewID generates a time-based session ID like "sim-20240409-143022".
func NewID() string {
	return time.Now().Format("sim-20060102-150405")
}

// Fork creates a copy of a session with a new ID (for variations).
func (m *Manager) Fork(id, newName string) (*Session, error) {
	src, err := m.Load(id)
	if err != nil {
		return nil, err
	}
	fork := *src
	fork.ID = NewID()
	fork.Name = newName
	fork.SimID = ""      // not yet run
	fork.CreatedAt = time.Time{}
	fork.UpdatedAt = time.Time{}
	// Reset runtime state for forked session
	fork.Status = ""
	fork.StartedAt = time.Time{}
	fork.FinishedAt = time.Time{}
	fork.ErrorMsg = ""
	fork.Progress = 0
	if err := m.Save(&fork); err != nil {
		return nil, err
	}
	return &fork, nil
}

// Patch applies field overrides to a session (used by `session modify`).
// Supported field names: scenario, rounds, platforms, timezone, notes, tags, name, status.
func (m *Manager) Patch(id string, fields map[string]string) (*Session, error) {
	s, err := m.Load(id)
	if err != nil {
		return nil, err
	}
	for k, v := range fields {
		switch k {
		case "scenario":
			s.Scenario = v
		case "rounds":
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("invalid rounds %q: %w", v, err)
			}
			s.Rounds = n
		case "platforms":
			var parts []string
			for _, p := range strings.Split(v, ",") {
				p = strings.TrimSpace(p)
				if p != "" {
					parts = append(parts, p)
				}
			}
			s.Platforms = parts
		case "timezone":
			s.TimeZone = v
		case "notes":
			s.Notes = v
		case "tags":
			var parts []string
			for _, t := range strings.Split(v, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					parts = append(parts, t)
				}
			}
			s.Tags = parts
		case "name":
			s.Name = v
		case "status":
			s.Status = v
		default:
			return nil, fmt.Errorf("unknown field %q", k)
		}
	}
	if err := m.Save(s); err != nil {
		return nil, err
	}
	return s, nil
}
