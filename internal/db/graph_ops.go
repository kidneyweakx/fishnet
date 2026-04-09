package db

import "database/sql"

// MigrateSchema adds new columns to edges and nodes if they don't already exist.
// SQLite doesn't support IF NOT EXISTS for ALTER TABLE, so errors are ignored
// (they simply indicate the column already exists).
func MigrateSchema(database *sql.DB) error {
	for _, stmt := range []string{
		`ALTER TABLE edges ADD COLUMN valid_at INTEGER`,
		`ALTER TABLE edges ADD COLUMN invalid_at INTEGER`,
		`ALTER TABLE nodes ADD COLUMN pagerank REAL DEFAULT 0.0`,
		`ALTER TABLE nodes ADD COLUMN entity_freq INTEGER DEFAULT 0`,
	} {
		database.Exec(stmt) // ignore errors (column already exists)
	}
	return nil
}

// ReplaceNodeInEdges updates all edges that reference oldNodeID to instead
// reference newNodeID (for both source_id and target_id columns).
func (d *DB) ReplaceNodeInEdges(oldNodeID, newNodeID string) error {
	if _, err := d.Exec(
		`UPDATE edges SET source_id = ? WHERE source_id = ?`,
		newNodeID, oldNodeID,
	); err != nil {
		return err
	}
	_, err := d.Exec(
		`UPDATE edges SET target_id = ? WHERE target_id = ?`,
		newNodeID, oldNodeID,
	)
	return err
}

// DeleteNode removes a node by its ID.
func (d *DB) DeleteNode(nodeID string) error {
	_, err := d.Exec(`DELETE FROM nodes WHERE id = ?`, nodeID)
	return err
}

// UpdatePageRank sets the pagerank score for a node.
func (d *DB) UpdatePageRank(nodeID string, score float64) error {
	_, err := d.Exec(`UPDATE nodes SET pagerank = ? WHERE id = ?`, score, nodeID)
	return err
}

// GetTopNodes returns nodes for a project ordered by pagerank descending.
func (d *DB) GetTopNodes(projectID string, limit int) ([]Node, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := d.Query(`
		SELECT id, project_id, name, type, COALESCE(summary,''), COALESCE(attributes,'{}'),
		       community_id, COALESCE(pagerank, 0.0)
		FROM nodes WHERE project_id = ?
		ORDER BY pagerank DESC
		LIMIT ?`, projectID, limit)
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
