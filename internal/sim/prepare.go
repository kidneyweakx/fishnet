package sim

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"fishnet/internal/db"
	"fishnet/internal/platform"
)

// PreparedPersonalities is the snapshot persisted after `sim prepare`.
type PreparedPersonalities struct {
	SessionID  string                  `json:"session_id"`
	Scenario   string                  `json:"scenario"`
	AgentCount int                     `json:"agent_count"`
	Data       []*platform.Personality `json:"data"`
}

// PersonaStore manages persistence of prepared personalities to
// .fishnet/sessions/<session-id>-personas.json.
type PersonaStore struct {
	dir string
}

// NewPersonaStore returns a store rooted at the working directory.
func NewPersonaStore(workDir string) *PersonaStore {
	return &PersonaStore{dir: filepath.Join(workDir, ".fishnet", "sessions")}
}

func (s *PersonaStore) path(sessionID string) string {
	return filepath.Join(s.dir, sessionID+"-personas.json")
}

// Save persists a PreparedPersonalities snapshot.
func (s *PersonaStore) Save(pp *PreparedPersonalities) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(pp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(pp.SessionID), data, 0644)
}

// Load retrieves a PreparedPersonalities snapshot for a session.
func (s *PersonaStore) Load(sessionID string) (*PreparedPersonalities, error) {
	data, err := os.ReadFile(s.path(sessionID))
	if err != nil {
		return nil, err
	}
	var pp PreparedPersonalities
	if err := json.Unmarshal(data, &pp); err != nil {
		return nil, err
	}
	return &pp, nil
}

// Exists returns true if a prepared snapshot exists for the session.
func (s *PersonaStore) Exists(sessionID string) bool {
	_, err := os.Stat(s.path(sessionID))
	return err == nil
}

// BuildPersonalities is the exported wrapper around the internal buildPersonalities.
// Called by `sim prepare` to build and persist personalities before running.
func (ps *PlatformSim) BuildPersonalities(
	ctx context.Context,
	nodes []db.Node,
	scenario string,
	concurrency int,
	simCfg *platform.SimConfig,
) []*platform.Personality {
	return ps.buildPersonalities(ctx, nodes, scenario, concurrency, simCfg)
}
