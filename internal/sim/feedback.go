package sim

import (
	"fmt"

	"fishnet/internal/db"
	"fishnet/internal/platform"
)

// GraphMemoryUpdater writes simulation actions back to the knowledge graph as new edges.
// This creates a feedback loop: simulation results enrich the graph.
type GraphMemoryUpdater struct {
	db *db.DB
}

// NewGraphMemoryUpdater returns a new GraphMemoryUpdater backed by database.
func NewGraphMemoryUpdater(database *db.DB) *GraphMemoryUpdater {
	return &GraphMemoryUpdater{db: database}
}

// ProcessAction converts a simulation action into a graph edge and upserts it.
// Maps action types to relationship types:
//
//	CREATE_POST  → agent PUBLISHED content (edge: agentID → PUBLISHED → agentID self-loop, fact = clipped content)
//	LIKE_POST    → agent ENDORSED content creator (edge: agentID → ENDORSED → postID)
//	REPOST       → agent AMPLIFIED content creator (edge: agentID → AMPLIFIED → postID)
//	FOLLOW       → agent FOLLOWS target agent (edge: agentID → FOLLOWS → postID which is target user ID)
//	COMMENT      → agent RESPONDED_TO content creator (edge: agentID → RESPONDED_TO → postID)
//	QUOTE_POST   → agent REFERENCED content creator (edge: agentID → REFERENCED → postID)
func (u *GraphMemoryUpdater) ProcessAction(projectID string, action platform.Action) error {
	switch action.Type {
	case "CREATE_POST":
		fact := clip(action.Content, 100)
		if fact == "" {
			fact = fmt.Sprintf("%s published content during simulation round %d", action.AgentName, action.Round)
		}
		return u.db.UpsertEdge(projectID, action.AgentID, action.AgentID, "PUBLISHED", fact)

	case "LIKE_POST":
		if action.PostID == "" {
			return nil
		}
		fact := fmt.Sprintf("%s endorsed content (post %s) during simulation round %d",
			action.AgentName, action.PostID, action.Round)
		return u.db.UpsertEdge(projectID, action.AgentID, action.PostID, "ENDORSED", fact)

	case "REPOST":
		if action.PostID == "" {
			return nil
		}
		fact := fmt.Sprintf("%s amplified content (post %s) during simulation round %d",
			action.AgentName, action.PostID, action.Round)
		return u.db.UpsertEdge(projectID, action.AgentID, action.PostID, "AMPLIFIED", fact)

	case "FOLLOW":
		if action.PostID == "" {
			return nil
		}
		fact := fmt.Sprintf("%s followed %s during simulation round %d",
			action.AgentName, action.PostID, action.Round)
		return u.db.UpsertEdge(projectID, action.AgentID, action.PostID, "FOLLOWS", fact)

	case "COMMENT":
		if action.PostID == "" {
			return nil
		}
		fact := fmt.Sprintf("%s responded to content (post %s) during simulation round %d",
			action.AgentName, action.PostID, action.Round)
		return u.db.UpsertEdge(projectID, action.AgentID, action.PostID, "RESPONDED_TO", fact)

	case "QUOTE_POST":
		if action.PostID == "" {
			return nil
		}
		fact := fmt.Sprintf("%s referenced content (post %s) during simulation round %d",
			action.AgentName, action.PostID, action.Round)
		return u.db.UpsertEdge(projectID, action.AgentID, action.PostID, "REFERENCED", fact)
	}
	return nil
}

// FlushActions processes a batch of actions at end of simulation.
func (u *GraphMemoryUpdater) FlushActions(projectID string, actions []platform.Action) error {
	for _, a := range actions {
		if err := u.ProcessAction(projectID, a); err != nil {
			return err
		}
	}
	return nil
}

// clip truncates s to at most n runes.
func clip(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
