package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const schema = `
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS projects (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL UNIQUE,
	source_dir TEXT,
	status     TEXT DEFAULT 'created',
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS documents (
	id          TEXT PRIMARY KEY,
	project_id  TEXT NOT NULL,
	path        TEXT NOT NULL,
	name        TEXT NOT NULL,
	content     TEXT,
	chunk_count INTEGER DEFAULT 0,
	processed   INTEGER DEFAULT 0,
	created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (project_id) REFERENCES projects(id)
);

CREATE TABLE IF NOT EXISTS chunks (
	id          TEXT PRIMARY KEY,
	doc_id      TEXT NOT NULL,
	project_id  TEXT NOT NULL,
	content     TEXT NOT NULL,
	chunk_index INTEGER,
	processed   INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS nodes (
	id           TEXT PRIMARY KEY,
	project_id   TEXT NOT NULL,
	name         TEXT NOT NULL,
	type         TEXT NOT NULL,
	summary      TEXT DEFAULT '',
	attributes   TEXT DEFAULT '{}',
	community_id INTEGER DEFAULT -1,
	created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(project_id, name, type)
);

CREATE TABLE IF NOT EXISTS edges (
	id         TEXT PRIMARY KEY,
	project_id TEXT NOT NULL,
	source_id  TEXT NOT NULL,
	target_id  TEXT NOT NULL,
	type       TEXT NOT NULL,
	fact       TEXT DEFAULT '',
	weight     REAL DEFAULT 1.0,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(project_id, source_id, target_id, type)
);

CREATE TABLE IF NOT EXISTS communities (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id TEXT NOT NULL,
	summary    TEXT DEFAULT '',
	node_count INTEGER DEFAULT 0,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS simulations (
	id         TEXT PRIMARY KEY,
	project_id TEXT NOT NULL,
	scenario   TEXT,
	status     TEXT DEFAULT 'pending',
	result     TEXT DEFAULT '',
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sim_posts (
    id          TEXT PRIMARY KEY,
    sim_id      TEXT NOT NULL,
    project_id  TEXT NOT NULL,
    platform    TEXT NOT NULL,
    author_id   TEXT NOT NULL,
    author_name TEXT NOT NULL,
    content     TEXT NOT NULL,
    parent_id   TEXT,
    subreddit   TEXT,
    tags        TEXT,
    likes       INTEGER DEFAULT 0,
    reposts     INTEGER DEFAULT 0,
    round       INTEGER NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sim_actions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    sim_id      TEXT NOT NULL,
    project_id  TEXT NOT NULL,
    platform    TEXT NOT NULL,
    round       INTEGER NOT NULL,
    agent_id    TEXT NOT NULL,
    agent_name  TEXT NOT NULL,
    action_type TEXT NOT NULL,
    post_id     TEXT,
    content     TEXT,
    success     INTEGER DEFAULT 1,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_nodes_project    ON nodes(project_id);
CREATE INDEX IF NOT EXISTS idx_nodes_community  ON nodes(community_id);
CREATE INDEX IF NOT EXISTS idx_edges_project    ON edges(project_id);
CREATE INDEX IF NOT EXISTS idx_edges_source     ON edges(source_id);
CREATE INDEX IF NOT EXISTS idx_edges_target     ON edges(target_id);
CREATE INDEX IF NOT EXISTS idx_chunks_project   ON chunks(project_id, processed);
CREATE INDEX IF NOT EXISTS idx_sim_posts_sim    ON sim_posts(sim_id);
CREATE INDEX IF NOT EXISTS idx_sim_actions_sim  ON sim_actions(sim_id);
CREATE INDEX IF NOT EXISTS idx_sim_actions_agent ON sim_actions(sim_id, agent_id);
`

// DB wraps sql.DB with graph-specific helpers.
type DB struct {
	*sql.DB
}

func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	raw, err := sql.Open("sqlite", path+"?_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(schema); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	// Run additive migrations (idempotent — errors mean column already exists).
	MigrateSchema(raw)
	return &DB{raw}, nil
}

// ─── Project ────────────────────────────────────────────────────────────────

func (db *DB) UpsertProject(name, dir string) (string, error) {
	var id string
	err := db.QueryRow(`SELECT id FROM projects WHERE name = ?`, name).Scan(&id)
	if err == sql.ErrNoRows {
		id = uuid.New().String()
		_, err = db.Exec(`INSERT INTO projects (id, name, source_dir) VALUES (?, ?, ?)`, id, name, dir)
		return id, err
	}
	if err != nil {
		return "", err
	}
	_, err = db.Exec(`UPDATE projects SET source_dir = ? WHERE id = ?`, dir, id)
	return id, err
}

func (db *DB) ProjectByName(name string) (string, error) {
	var id string
	err := db.QueryRow(`SELECT id FROM projects WHERE name = ?`, name).Scan(&id)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("project %q not found; run: fishnet init %s", name, name)
	}
	return id, err
}

func (db *DB) ListProjects() ([]struct{ Name, Status string }, error) {
	rows, err := db.Query(`SELECT name, status FROM projects ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []struct{ Name, Status string }
	for rows.Next() {
		var r struct{ Name, Status string }
		rows.Scan(&r.Name, &r.Status)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ─── Documents & Chunks ─────────────────────────────────────────────────────

func (db *DB) AddDocument(projectID, path, name, content string, chunkCount int) (string, error) {
	id := uuid.New().String()
	_, err := db.Exec(`
		INSERT INTO documents (id, project_id, path, name, content, chunk_count)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`,
		id, projectID, path, name, content, chunkCount)
	return id, err
}

func (db *DB) AddChunk(docID, projectID, content string, idx int) error {
	_, err := db.Exec(`
		INSERT INTO chunks (id, doc_id, project_id, content, chunk_index)
		VALUES (?, ?, ?, ?, ?)`,
		uuid.New().String(), docID, projectID, content, idx)
	return err
}

type Chunk struct {
	ID      string
	Content string
}

func (db *DB) UnprocessedChunks(projectID string) ([]Chunk, error) {
	rows, err := db.Query(
		`SELECT id, content FROM chunks WHERE project_id = ? AND processed = 0 ORDER BY chunk_index`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chunk
	for rows.Next() {
		var c Chunk
		rows.Scan(&c.ID, &c.Content)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (db *DB) MarkChunkDone(id string) error {
	_, err := db.Exec(`UPDATE chunks SET processed = 1 WHERE id = ?`, id)
	return err
}

// ─── Nodes ──────────────────────────────────────────────────────────────────

type Node struct {
	ID          string
	ProjectID   string
	Name        string
	Type        string
	Summary     string
	Attributes  string // JSON
	CommunityID int
	PageRank    float64
	CreatedAt   time.Time
}

// UpsertNode inserts or updates by (project_id, name, type). Returns the node ID.
func (db *DB) UpsertNode(projectID, name, nodeType, summary, attrs string) (string, error) {
	var id string
	err := db.QueryRow(
		`SELECT id FROM nodes WHERE project_id = ? AND name = ? AND type = ?`,
		projectID, name, nodeType).Scan(&id)

	if err == sql.ErrNoRows {
		id = uuid.New().String()
		_, err = db.Exec(`
			INSERT INTO nodes (id, project_id, name, type, summary, attributes)
			VALUES (?, ?, ?, ?, ?, ?)`,
			id, projectID, name, nodeType, summary, attrs)
		return id, err
	}
	if err != nil {
		return "", err
	}
	if summary != "" {
		_, err = db.Exec(`UPDATE nodes SET summary = ? WHERE id = ?`, summary, id)
	}
	return id, err
}

func (db *DB) GetNodes(projectID string) ([]Node, error) {
	rows, err := db.Query(`
		SELECT id, project_id, name, type, COALESCE(summary,''), COALESCE(attributes,'{}'),
		       community_id, COALESCE(pagerank, 0.0)
		FROM nodes WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		rows.Scan(&n.ID, &n.ProjectID, &n.Name, &n.Type, &n.Summary, &n.Attributes, &n.CommunityID, &n.PageRank)
		out = append(out, n)
	}
	return out, rows.Err()
}

func (db *DB) UpdateCommunity(nodeID string, communityID int) error {
	_, err := db.Exec(`UPDATE nodes SET community_id = ? WHERE id = ?`, communityID, nodeID)
	return err
}

func (db *DB) GetNode(nodeID string) (Node, error) {
	var n Node
	err := db.QueryRow(`
		SELECT id, project_id, name, type, COALESCE(summary,''), COALESCE(attributes,'{}'),
		       community_id, COALESCE(pagerank, 0.0)
		FROM nodes WHERE id = ?`, nodeID).
		Scan(&n.ID, &n.ProjectID, &n.Name, &n.Type, &n.Summary, &n.Attributes, &n.CommunityID, &n.PageRank)
	return n, err
}

func (db *DB) UpdateNodeAttributes(nodeID, attributes string) error {
	_, err := db.Exec(`UPDATE nodes SET attributes = ? WHERE id = ?`, attributes, nodeID)
	return err
}

// UpdateNode updates the name, type, summary, and attributes of a node.
func (db *DB) UpdateNode(nodeID, name, nodeType, summary, attrs string) error {
	_, err := db.Exec(
		`UPDATE nodes SET name = ?, type = ?, summary = ?, attributes = ? WHERE id = ?`,
		name, nodeType, summary, attrs, nodeID)
	return err
}

// InsertNode creates a new node with a fresh UUID and returns its ID.
func (db *DB) InsertNode(projectID, name, nodeType, summary, attrs string) (string, error) {
	id := uuid.New().String()
	_, err := db.Exec(
		`INSERT INTO nodes (id, project_id, name, type, summary, attributes) VALUES (?, ?, ?, ?, ?, ?)`,
		id, projectID, name, nodeType, summary, attrs)
	return id, err
}

// MergeNodes rewires all edges from dropID to keepID then deletes dropID.
// Self-loops created by the rewiring are removed automatically.
func (db *DB) MergeNodes(keepID, dropID string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE edges SET source_id = ? WHERE source_id = ?`, keepID, dropID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE edges SET target_id = ? WHERE target_id = ?`, keepID, dropID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM edges WHERE source_id = ? AND target_id = ?`, keepID, keepID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM nodes WHERE id = ?`, dropID); err != nil {
		return err
	}
	return tx.Commit()
}

// ─── Edges ──────────────────────────────────────────────────────────────────

type Edge struct {
	ID        string
	ProjectID string
	SourceID  string
	TargetID  string
	Type      string
	Fact      string
	Weight    float64
}

func (db *DB) UpsertEdge(projectID, sourceID, targetID, edgeType, fact string) error {
	_, err := db.Exec(`
		INSERT INTO edges (id, project_id, source_id, target_id, type, fact)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(project_id, source_id, target_id, type) DO UPDATE SET
			fact = CASE WHEN excluded.fact != '' THEN excluded.fact ELSE fact END,
			weight = weight + 0.1`,
		uuid.New().String(), projectID, sourceID, targetID, edgeType, fact)
	return err
}

func (db *DB) GetEdges(projectID string) ([]Edge, error) {
	rows, err := db.Query(`
		SELECT id, project_id, source_id, target_id, type, COALESCE(fact,''), weight
		FROM edges WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Edge
	for rows.Next() {
		var e Edge
		rows.Scan(&e.ID, &e.ProjectID, &e.SourceID, &e.TargetID, &e.Type, &e.Fact, &e.Weight)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ─── Stats ──────────────────────────────────────────────────────────────────

type Stats struct {
	Nodes       int
	Edges       int
	Documents   int
	Chunks      int
	Communities int
}

func (db *DB) GetStats(projectID string) Stats {
	var s Stats
	db.QueryRow(`SELECT COUNT(*) FROM nodes WHERE project_id = ?`, projectID).Scan(&s.Nodes)
	db.QueryRow(`SELECT COUNT(*) FROM edges WHERE project_id = ?`, projectID).Scan(&s.Edges)
	db.QueryRow(`SELECT COUNT(*) FROM documents WHERE project_id = ?`, projectID).Scan(&s.Documents)
	db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE project_id = ?`, projectID).Scan(&s.Chunks)
	db.QueryRow(`SELECT COUNT(DISTINCT community_id) FROM nodes WHERE project_id = ? AND community_id >= 0`, projectID).Scan(&s.Communities)
	return s
}

// ─── Simulations ────────────────────────────────────────────────────────────

func (db *DB) CreateSim(projectID, scenario string) (string, error) {
	id := uuid.New().String()
	_, err := db.Exec(`
		INSERT INTO simulations (id, project_id, scenario, status)
		VALUES (?, ?, ?, 'running')`, id, projectID, scenario)
	return id, err
}

func (db *DB) FinishSim(id, result string) error {
	_, err := db.Exec(`UPDATE simulations SET status = 'done', result = ? WHERE id = ?`, result, id)
	return err
}

func (db *DB) GetSimResult(id string) (string, error) {
	var result string
	err := db.QueryRow(`SELECT result FROM simulations WHERE id = ?`, id).Scan(&result)
	return result, err
}

// ─── Sim Posts & Actions ─────────────────────────────────────────────────────

// SimPost mirrors the sim_posts table row.
type SimPost struct {
	ID         string
	SimID      string
	ProjectID  string
	Platform   string
	AuthorID   string
	AuthorName string
	Content    string
	ParentID   string
	Subreddit  string
	Tags       []string
	Likes      int
	Reposts    int
	Round      int
	CreatedAt  string
}

// SimAction mirrors the sim_actions table row.
type SimAction struct {
	ID         int64
	SimID      string
	ProjectID  string
	Platform   string
	Round      int
	AgentID    string
	AgentName  string
	ActionType string
	PostID     string
	Content    string
	Success    bool
	CreatedAt  string
}

// AgentStat holds per-agent action aggregates for a simulation.
type AgentStat struct {
	AgentID       string
	AgentName     string
	TotalPosts    int
	TotalLikes    int
	TotalReposts  int
	TotalComments int
}

// SimRecord holds summary info about a simulation run.
type SimRecord struct {
	ID        string
	ProjectID string
	Scenario  string
	CreatedAt string
	Done      bool
}

// SaveSimPost inserts a post from simulation results.
// simID, projectID, platform: identifiers; authorID/authorName/content: post data;
// parentID/subreddit: optional; tags: JSON-encoded array; likes/reposts/round: metrics.
func (d *DB) SaveSimPost(simID, projectID, platform, authorID, authorName, content, parentID, subreddit string, tags []string, likes, reposts, round int) error {
	tagsJSON := "[]"
	if len(tags) > 0 {
		b, _ := json.Marshal(tags)
		tagsJSON = string(b)
	}
	_, err := d.Exec(`
		INSERT INTO sim_posts (id, sim_id, project_id, platform, author_id, author_name, content, parent_id, subreddit, tags, likes, reposts, round)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		uuid.New().String(), simID, projectID, platform, authorID, authorName, content,
		parentID, subreddit, tagsJSON, likes, reposts, round)
	return err
}

// SaveSimAction inserts an action from simulation results.
func (d *DB) SaveSimAction(simID, projectID, platform string, round int, agentID, agentName, actionType, postID, content string, success bool) error {
	successInt := 0
	if success {
		successInt = 1
	}
	_, err := d.Exec(`
		INSERT INTO sim_actions (sim_id, project_id, platform, round, agent_id, agent_name, action_type, post_id, content, success)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		simID, projectID, platform, round, agentID, agentName, actionType, postID, content, successInt)
	return err
}

// GetSimPosts returns posts for a simulation, optionally filtered by platform.
func (d *DB) GetSimPosts(simID, plt string, limit int) ([]SimPost, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, sim_id, project_id, platform, author_id, author_name, content,
	             COALESCE(parent_id,''), COALESCE(subreddit,''), COALESCE(tags,'[]'),
	             likes, reposts, round, created_at
	      FROM sim_posts WHERE sim_id = ?`
	args := []any{simID}
	if plt != "" {
		q += " AND platform = ?"
		args = append(args, plt)
	}
	q += " ORDER BY round, created_at LIMIT ?"
	args = append(args, limit)

	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SimPost
	for rows.Next() {
		var p SimPost
		var tagsJSON string
		if err := rows.Scan(&p.ID, &p.SimID, &p.ProjectID, &p.Platform,
			&p.AuthorID, &p.AuthorName, &p.Content, &p.ParentID, &p.Subreddit,
			&tagsJSON, &p.Likes, &p.Reposts, &p.Round, &p.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(tagsJSON), &p.Tags)
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetSimActions returns actions for a simulation, optionally filtered by agent or type.
func (d *DB) GetSimActions(simID, agentID, actionType string, limit int) ([]SimAction, error) {
	if limit <= 0 {
		limit = 100
	}
	var conditions []string
	args := []any{simID}
	conditions = append(conditions, "sim_id = ?")

	if agentID != "" {
		conditions = append(conditions, "agent_id = ?")
		args = append(args, agentID)
	}
	if actionType != "" {
		conditions = append(conditions, "action_type = ?")
		args = append(args, actionType)
	}
	q := `SELECT id, sim_id, project_id, platform, round, agent_id, agent_name, action_type,
	             COALESCE(post_id,''), COALESCE(content,''), success, created_at
	      FROM sim_actions WHERE ` + strings.Join(conditions, " AND ") +
		" ORDER BY round, id LIMIT ?"
	args = append(args, limit)

	rows, err := d.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SimAction
	for rows.Next() {
		var a SimAction
		var successInt int
		if err := rows.Scan(&a.ID, &a.SimID, &a.ProjectID, &a.Platform, &a.Round,
			&a.AgentID, &a.AgentName, &a.ActionType, &a.PostID, &a.Content,
			&successInt, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Success = successInt == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetSimTimeline returns a chronological mix of actions across platforms.
func (d *DB) GetSimTimeline(simID string, limit int) ([]SimAction, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.Query(`
		SELECT id, sim_id, project_id, platform, round, agent_id, agent_name, action_type,
		       COALESCE(post_id,''), COALESCE(content,''), success, created_at
		FROM sim_actions WHERE sim_id = ?
		ORDER BY round, id LIMIT ?`, simID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SimAction
	for rows.Next() {
		var a SimAction
		var successInt int
		if err := rows.Scan(&a.ID, &a.SimID, &a.ProjectID, &a.Platform, &a.Round,
			&a.AgentID, &a.AgentName, &a.ActionType, &a.PostID, &a.Content,
			&successInt, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Success = successInt == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAgentStats returns per-agent action counts for a simulation.
func (d *DB) GetAgentStats(simID string) ([]AgentStat, error) {
	rows, err := d.Query(`
		SELECT agent_id, agent_name,
		       SUM(CASE WHEN action_type = 'CREATE_POST' THEN 1 ELSE 0 END) AS total_posts,
		       SUM(CASE WHEN action_type = 'LIKE_POST' THEN 1 ELSE 0 END) AS total_likes,
		       SUM(CASE WHEN action_type = 'REPOST' THEN 1 ELSE 0 END) AS total_reposts,
		       SUM(CASE WHEN action_type = 'COMMENT' THEN 1 ELSE 0 END) AS total_comments
		FROM sim_actions WHERE sim_id = ?
		GROUP BY agent_id, agent_name
		ORDER BY total_posts DESC`, simID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AgentStat
	for rows.Next() {
		var s AgentStat
		if err := rows.Scan(&s.AgentID, &s.AgentName, &s.TotalPosts, &s.TotalLikes, &s.TotalReposts, &s.TotalComments); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSimsByProject returns all sim IDs for a project (most recent first).
func (d *DB) GetSimsByProject(projectID string, limit int) ([]SimRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := d.Query(`
		SELECT id, project_id, COALESCE(scenario,''), created_at, status = 'done'
		FROM simulations WHERE project_id = ?
		ORDER BY created_at DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SimRecord
	for rows.Next() {
		var r SimRecord
		var doneInt int
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Scenario, &r.CreatedAt, &doneInt); err != nil {
			return nil, err
		}
		r.Done = doneInt == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// ─── Danger Zone ─────────────────────────────────────────────────────────────

// ClearGraph deletes all nodes, edges, and communities for a project.
func (d *DB) ClearGraph(projectID string) error {
	_, err := d.Exec(`DELETE FROM edges WHERE project_id = ?`, projectID)
	if err != nil {
		return err
	}
	_, err = d.Exec(`DELETE FROM nodes WHERE project_id = ?`, projectID)
	if err != nil {
		return err
	}
	_, err = d.Exec(`DELETE FROM communities WHERE project_id = ?`, projectID)
	return err
}

// ClearSimData deletes all simulation posts and actions for a project.
func (d *DB) ClearSimData(projectID string) error {
	simRows, err := d.Query(`SELECT id FROM simulations WHERE project_id = ?`, projectID)
	if err != nil {
		return err
	}
	var ids []string
	for simRows.Next() {
		var id string
		simRows.Scan(&id)
		ids = append(ids, id)
	}
	simRows.Close()
	for _, id := range ids {
		d.Exec(`DELETE FROM sim_posts WHERE sim_id = ?`, id)
		d.Exec(`DELETE FROM sim_actions WHERE sim_id = ?`, id)
	}
	_, err = d.Exec(`DELETE FROM simulations WHERE project_id = ?`, projectID)
	return err
}

// ClearDocuments deletes all documents and chunks for a project.
func (d *DB) ClearDocuments(projectID string) error {
	_, err := d.Exec(`DELETE FROM chunks WHERE project_id = ?`, projectID)
	if err != nil {
		return err
	}
	_, err = d.Exec(`DELETE FROM documents WHERE project_id = ?`, projectID)
	return err
}
