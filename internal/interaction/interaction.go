// Package interaction manages persistent interaction sessions:
// agent interviews, surveys, and report-agent chats.
package interaction

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fishnet/internal/llm"
)

// Session types.
const (
	TypeInterview  = "interview"
	TypeSurvey     = "survey"
	TypeReportChat = "report_chat"
)

// SurveyAnswer is one agent's response in a survey.
type SurveyAnswer struct {
	AgentName string `json:"agent_name"`
	AgentType string `json:"agent_type"`
	Response  string `json:"response"`
}

// Session stores a complete interaction record that can be resumed.
type Session struct {
	ID        string         `json:"id"`         // "survey-20240409-143022"
	Type      string         `json:"type"`       // interview|survey|report_chat
	AgentName string         `json:"agent_name"` // for interview; empty for others
	ReportID  string         `json:"report_id"`  // for report_chat
	Question  string         `json:"question"`   // seed question or survey prompt
	Answers   []SurveyAnswer `json:"answers"`    // survey results
	History   []llm.Message  `json:"history"`    // full multi-turn conversation
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Manager handles Session persistence in .fishnet/interactions/.
type Manager struct {
	dir string
}

// NewManager creates a Manager rooted at the given working directory.
func NewManager(workDir string) *Manager {
	return &Manager{dir: filepath.Join(workDir, ".fishnet", "interactions")}
}

func (m *Manager) ensureDir() error {
	return os.MkdirAll(m.dir, 0755)
}

func (m *Manager) path(id string) string {
	return filepath.Join(m.dir, id+".json")
}

// NewID generates a type-prefixed time-based ID.
func NewID(sessionType string) string {
	prefix := "int"
	switch sessionType {
	case TypeSurvey:
		prefix = "survey"
	case TypeReportChat:
		prefix = "rchat"
	}
	return time.Now().Format(prefix + "-20060102-150405")
}

// Create creates and persists a new Session.
func (m *Manager) Create(sessionType, agentName, question string) (*Session, error) {
	if err := m.ensureDir(); err != nil {
		return nil, err
	}
	s := &Session{
		ID:        NewID(sessionType),
		Type:      sessionType,
		AgentName: agentName,
		Question:  question,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	return s, m.Save(s)
}

// Save persists the session to disk.
func (m *Manager) Save(s *Session) error {
	if err := m.ensureDir(); err != nil {
		return err
	}
	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path(s.ID), data, 0644)
}

// Load retrieves a Session by exact ID or prefix match.
func (m *Manager) Load(ref string) (*Session, error) {
	if data, err := os.ReadFile(m.path(ref)); err == nil {
		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			return nil, err
		}
		return &s, nil
	}
	all, err := m.List()
	if err != nil {
		return nil, err
	}
	refLow := strings.ToLower(ref)
	for _, s := range all {
		if strings.HasPrefix(strings.ToLower(s.ID), refLow) {
			return s, nil
		}
	}
	return nil, fmt.Errorf("interaction %q not found", ref)
}

// List returns all Sessions sorted by CreatedAt descending.
func (m *Manager) List() ([]*Session, error) {
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
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
