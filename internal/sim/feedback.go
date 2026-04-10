package sim

import (
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
// Uses the Action.Description() method for the fact text, and maps action types
// to semantic relationship types in the knowledge graph.
func (u *GraphMemoryUpdater) ProcessAction(projectID string, action platform.Action) error {
	fact := action.Description()

	switch action.Type {
	case platform.ActCreatePost:
		return u.db.UpsertEdge(projectID, action.AgentID, action.AgentID, "PUBLISHED", fact)

	case platform.ActCreateComment:
		if action.PostID == "" {
			return nil
		}
		return u.db.UpsertEdge(projectID, action.AgentID, action.PostID, "RESPONDED_TO", fact)

	case platform.ActLikePost:
		if action.PostID == "" {
			return nil
		}
		return u.db.UpsertEdge(projectID, action.AgentID, action.PostID, "ENDORSED", fact)

	case platform.ActDislikePost:
		if action.PostID == "" {
			return nil
		}
		return u.db.UpsertEdge(projectID, action.AgentID, action.PostID, "OPPOSED", fact)

	case platform.ActLikeComment:
		if action.PostID == "" {
			return nil
		}
		return u.db.UpsertEdge(projectID, action.AgentID, action.PostID, "ENDORSED_COMMENT", fact)

	case platform.ActDislikeComment:
		if action.PostID == "" {
			return nil
		}
		return u.db.UpsertEdge(projectID, action.AgentID, action.PostID, "OPPOSED_COMMENT", fact)

	case platform.ActRepost:
		if action.PostID == "" {
			return nil
		}
		return u.db.UpsertEdge(projectID, action.AgentID, action.PostID, "AMPLIFIED", fact)

	case platform.ActQuotePost:
		if action.PostID == "" {
			return nil
		}
		return u.db.UpsertEdge(projectID, action.AgentID, action.PostID, "REFERENCED", fact)

	case platform.ActFollow:
		target := action.TargetID
		if target == "" {
			target = action.PostID // backwards compat
		}
		if target == "" {
			return nil
		}
		return u.db.UpsertEdge(projectID, action.AgentID, target, "FOLLOWS", fact)

	case platform.ActMute:
		target := action.TargetID
		if target == "" {
			return nil
		}
		return u.db.UpsertEdge(projectID, action.AgentID, target, "MUTED", fact)

	case platform.ActSearchPosts:
		return u.db.UpsertEdge(projectID, action.AgentID, action.AgentID, "SEARCHED", fact)

	case platform.ActSearchUser:
		return u.db.UpsertEdge(projectID, action.AgentID, action.AgentID, "SEARCHED_USER", fact)

	case platform.ActTrend, platform.ActRefresh, platform.ActDoNothing:
		// Passive actions — no graph edge needed
		return nil
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
