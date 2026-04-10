package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// GraphTask tracks a single graph-build run (analyze pipeline).
type GraphTask struct {
	ID           string    `json:"id"`            // "task-20240409-143022"
	Dir          string    `json:"dir"`           // source documents directory
	ProjectID    string    `json:"project_id"`
	Status       string    `json:"status"`        // pending|running|completed|failed|interrupted
	ChunksDone   int64     `json:"chunks_done"`
	ChunksTotal  int64     `json:"chunks_total"`
	NodesAdded   int64     `json:"nodes_added"`
	EdgesAdded   int64     `json:"edges_added"`
	Errors       int64     `json:"errors"`
	HasOntology  bool      `json:"has_ontology"`
	HasCommunity bool      `json:"has_community"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	ErrorMsg     string    `json:"error_msg"`
}

// Progress returns 0-100 percent complete.
func (t *GraphTask) Progress() int {
	if t.ChunksTotal <= 0 {
		return 0
	}
	pct := int(t.ChunksDone * 100 / t.ChunksTotal)
	if pct > 100 {
		return 100
	}
	return pct
}

func (t *GraphTask) MarkRunning() {
	t.Status = "running"
	t.StartedAt = time.Now()
	t.ErrorMsg = ""
}

func (t *GraphTask) MarkCompleted() {
	t.Status = "completed"
	t.FinishedAt = time.Now()
	t.ChunksDone = t.ChunksTotal
}

func (t *GraphTask) MarkFailed(msg string) {
	t.Status = "failed"
	t.FinishedAt = time.Now()
	t.ErrorMsg = msg
}

func (t *GraphTask) MarkInterrupted() {
	t.Status = "interrupted"
	t.FinishedAt = time.Now()
}

// Manager handles GraphTask persistence in .fishnet/tasks/.
type Manager struct {
	dir string
}

// NewManager creates a Manager rooted at the given working directory.
func NewManager(workDir string) *Manager {
	return &Manager{dir: filepath.Join(workDir, ".fishnet", "tasks")}
}

func (m *Manager) ensureDir() error {
	return os.MkdirAll(m.dir, 0755)
}

func (m *Manager) path(id string) string {
	return filepath.Join(m.dir, id+".json")
}

// NewID generates a time-based task ID like "task-20240409-143022".
func NewID() string {
	return time.Now().Format("task-20060102-150405")
}

// Create creates and persists a new task.
func (m *Manager) Create(projectID, dir string, chunksTotal int64, hasOntology, hasCommunity bool) (*GraphTask, error) {
	if err := m.ensureDir(); err != nil {
		return nil, err
	}
	t := &GraphTask{
		ID:           NewID(),
		Dir:          dir,
		ProjectID:    projectID,
		Status:       "pending",
		ChunksTotal:  chunksTotal,
		HasOntology:  hasOntology,
		HasCommunity: hasCommunity,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	return t, m.Save(t)
}

// Save persists the task to disk.
func (m *Manager) Save(t *GraphTask) error {
	if err := m.ensureDir(); err != nil {
		return err
	}
	t.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path(t.ID), data, 0644)
}

// Load retrieves a task by exact ID or prefix match.
func (m *Manager) Load(ref string) (*GraphTask, error) {
	if data, err := os.ReadFile(m.path(ref)); err == nil {
		var t GraphTask
		if err := json.Unmarshal(data, &t); err != nil {
			return nil, err
		}
		return &t, nil
	}
	all, err := m.List()
	if err != nil {
		return nil, err
	}
	refLow := strings.ToLower(ref)
	for _, t := range all {
		if strings.HasPrefix(strings.ToLower(t.ID), refLow) {
			return t, nil
		}
	}
	return nil, fmt.Errorf("task %q not found", ref)
}

// List returns all tasks sorted by CreatedAt descending.
func (m *Manager) List() ([]*GraphTask, error) {
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
	var out []*GraphTask
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			continue
		}
		var t GraphTask
		if err := json.Unmarshal(data, &t); err != nil {
			continue
		}
		out = append(out, &t)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
