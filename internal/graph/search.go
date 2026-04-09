package graph

import (
	"context"
	"fmt"
	"strings"

	"fishnet/internal/db"
	"fishnet/internal/llm"
)

// SearchResult holds nodes, edges, and synthesized facts from a graph search.
type SearchResult struct {
	Nodes []db.Node
	Edges []db.Edge
	Facts []string // one-line facts derived from edges
	Query string
}

// QuickSearch performs a keyword search on node names, types, and summaries,
// plus edge fact fields, using case-insensitive substring matching.
func QuickSearch(database *db.DB, projectID, query string, limit int) SearchResult {
	result := SearchResult{Query: query}

	keywords := tokenize(query)
	if len(keywords) == 0 {
		return result
	}

	allNodes, err := database.GetNodes(projectID)
	if err != nil {
		return result
	}
	allEdges, err := database.GetEdges(projectID)
	if err != nil {
		return result
	}

	// Build node index for quick lookup
	nodeByID := make(map[string]db.Node, len(allNodes))
	for _, n := range allNodes {
		nodeByID[n.ID] = n
	}

	// Match nodes where ANY keyword appears in name, type, or summary
	nodeSet := make(map[string]db.Node)
	for _, n := range allNodes {
		if matchesAny(keywords, n.Name, n.Type, n.Summary) {
			nodeSet[n.ID] = n
		}
	}

	// Collect matched nodes (up to limit)
	for _, n := range nodeSet {
		if limit > 0 && len(result.Nodes) >= limit {
			break
		}
		result.Nodes = append(result.Nodes, n)
	}

	// Collect edges where both endpoints are matched nodes or edge fact matches
	edgeSet := make(map[string]db.Edge)
	for _, e := range allEdges {
		_, srcMatched := nodeSet[e.SourceID]
		_, tgtMatched := nodeSet[e.TargetID]
		factMatched := matchesAny(keywords, e.Fact, e.Type)
		if (srcMatched || tgtMatched) || factMatched {
			edgeSet[e.ID] = e
		}
	}

	for _, e := range edgeSet {
		result.Edges = append(result.Edges, e)
		if e.Fact != "" {
			src := nodeByID[e.SourceID]
			tgt := nodeByID[e.TargetID]
			fact := fmt.Sprintf("[%s] %s --%s--> %s: %s",
				e.Type, src.Name, e.Type, tgt.Name, e.Fact)
			result.Facts = append(result.Facts, fact)
		}
	}

	return result
}

// PanoramaSearch performs a broad search returning all nodes matching any keyword,
// plus all their connected edges and the nodes at those edge endpoints.
func PanoramaSearch(database *db.DB, projectID, query string, limit int) SearchResult {
	result := SearchResult{Query: query}

	keywords := tokenize(query)
	if len(keywords) == 0 {
		return result
	}

	allNodes, err := database.GetNodes(projectID)
	if err != nil {
		return result
	}
	allEdges, err := database.GetEdges(projectID)
	if err != nil {
		return result
	}

	// Build node index
	nodeByID := make(map[string]db.Node, len(allNodes))
	for _, n := range allNodes {
		nodeByID[n.ID] = n
	}

	// Match nodes
	seedSet := make(map[string]db.Node)
	for _, n := range allNodes {
		if matchesAny(keywords, n.Name, n.Type, n.Summary) {
			seedSet[n.ID] = n
		}
	}

	// Find all edges touching matched nodes, collect neighbor nodes too
	finalNodes := make(map[string]db.Node)
	finalEdges := make(map[string]db.Edge)

	for id, n := range seedSet {
		finalNodes[id] = n
	}

	for _, e := range allEdges {
		_, srcSeed := seedSet[e.SourceID]
		_, tgtSeed := seedSet[e.TargetID]
		if srcSeed || tgtSeed {
			finalEdges[e.ID] = e
			// Pull in neighbor nodes
			if n, ok := nodeByID[e.SourceID]; ok {
				finalNodes[n.ID] = n
			}
			if n, ok := nodeByID[e.TargetID]; ok {
				finalNodes[n.ID] = n
			}
		}
	}

	// Populate result (respecting limit for nodes)
	for _, n := range finalNodes {
		if limit > 0 && len(result.Nodes) >= limit {
			break
		}
		result.Nodes = append(result.Nodes, n)
	}

	for _, e := range finalEdges {
		result.Edges = append(result.Edges, e)
		if e.Fact != "" {
			src := nodeByID[e.SourceID]
			tgt := nodeByID[e.TargetID]
			fact := fmt.Sprintf("[%s] %s --%s--> %s: %s",
				e.Type, src.Name, e.Type, tgt.Name, e.Fact)
			result.Facts = append(result.Facts, fact)
		}
	}

	return result
}

// InsightForge uses an LLM to decompose the query into 3-5 sub-questions,
// runs QuickSearch for each, then merges and deduplicates results by node ID.
// Also runs a direct search on the original query and merges those results,
// ensuring broader coverage with relevance-scored deduplication.
func InsightForge(ctx context.Context, database *db.DB, llmClient *llm.Client, projectID, query string) (SearchResult, error) {
	merged := SearchResult{Query: query}

	// Ask LLM to generate 3-5 sub-questions covering different dimensions of the query.
	const insightSystem = `You are a graph search assistant. Given a research question, generate 3 to 5 focused sub-questions that together cover different angles (who, what, why, how, when) of the original question. Return ONLY a JSON array of strings (no markdown, no explanation). Example:
["Who are the key actors?", "What organizations are involved?", "What are the main conflicts?", "What outcomes occurred?", "How did relationships change?"]`

	var subQuestions []string
	err := llmClient.JSON(ctx, insightSystem,
		fmt.Sprintf("Generate 3-5 sub-questions for: %s", query),
		&subQuestions)
	if err != nil || len(subQuestions) == 0 {
		// Fall back to single quick search on the original query.
		r := QuickSearch(database, projectID, query, 20)
		return r, nil
	}

	// Clamp to at most 5 sub-questions.
	if len(subQuestions) > 5 {
		subQuestions = subQuestions[:5]
	}

	// Deduplication sets.
	seenNodes := make(map[string]bool)
	seenEdges := make(map[string]bool)
	seenFacts := make(map[string]bool)

	mergeResult := func(r SearchResult) {
		for _, n := range r.Nodes {
			if !seenNodes[n.ID] {
				seenNodes[n.ID] = true
				merged.Nodes = append(merged.Nodes, n)
			}
		}
		for _, e := range r.Edges {
			if !seenEdges[e.ID] {
				seenEdges[e.ID] = true
				merged.Edges = append(merged.Edges, e)
			}
		}
		for _, f := range r.Facts {
			if !seenFacts[f] {
				seenFacts[f] = true
				merged.Facts = append(merged.Facts, f)
			}
		}
	}

	// Run QuickSearch for each sub-question and merge.
	for _, sq := range subQuestions {
		mergeResult(QuickSearch(database, projectID, sq, 10))
	}

	// Also search on the original query to capture direct matches.
	mergeResult(QuickSearch(database, projectID, query, 20))

	return merged, nil
}

// tokenize splits a query into lowercase keywords, filtering blanks.
func tokenize(query string) []string {
	raw := strings.Fields(strings.ToLower(query))
	var out []string
	for _, w := range raw {
		if len(w) >= 2 {
			out = append(out, w)
		}
	}
	return out
}

// matchesAny returns true if any keyword appears (case-insensitive) in any of the fields.
func matchesAny(keywords []string, fields ...string) bool {
	for _, field := range fields {
		lower := strings.ToLower(field)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				return true
			}
		}
	}
	return false
}
