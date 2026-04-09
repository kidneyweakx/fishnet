package graph

import (
	"context"
	"fmt"
	"sort"
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

// QuickSearch performs a TF-IDF scored search on node names, types, and summaries,
// plus edge fact fields. Results are ranked by (tfidf*0.6 + subBonus*0.3 + pagerank*0.1).
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

	// Build TF-IDF scorer
	scorer := BuildTFIDF(allNodes)

	// Score each node: TF-IDF × 0.6 + substring bonus × 0.3 + PageRank × 0.1
	// PageRank is read from n.PageRank (persisted to DB after each analyze/init).
	// Fall back to in-memory computation only when all pageranks are zero (first run).
	hasStoredPR := false
	for _, n := range allNodes {
		if n.PageRank > 0 {
			hasStoredPR = true
			break
		}
	}
	var livePR map[string]float64
	if !hasStoredPR {
		livePR = ComputePageRank(allNodes, allEdges)
	}

	type scoredNode struct {
		node  db.Node
		score float64
	}
	var scored []scoredNode

	for _, n := range allNodes {
		tfidf := scorer(n.ID, query)
		subMatch := matchesAny(keywords, n.Name, n.Type, n.Summary)
		if tfidf <= 0 && !subMatch {
			continue
		}
		subBonus := 0.0
		if subMatch {
			subBonus = 0.3
		}
		pr := n.PageRank
		if !hasStoredPR {
			pr = livePR[n.ID]
		}
		score := tfidf*0.6 + subBonus*0.3 + pr*0.1
		scored = append(scored, scoredNode{n, score})
	}

	// Sort by score DESC
	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })

	// Build node set for edge lookup (all scored nodes, not limited yet)
	nodeSet := make(map[string]db.Node, len(scored))
	for _, sn := range scored {
		nodeSet[sn.node.ID] = sn.node
	}

	// Collect matched nodes (up to limit)
	for _, sn := range scored {
		if limit > 0 && len(result.Nodes) >= limit {
			break
		}
		result.Nodes = append(result.Nodes, sn.node)
	}

	// Collect edges where both endpoints are matched nodes or edge fact matches
	edgeSet := make(map[string]db.Edge)
	for _, e := range allEdges {
		_, srcMatched := nodeSet[e.SourceID]
		_, tgtMatched := nodeSet[e.TargetID]
		factMatched := matchesAny(keywords, e.Fact, e.Type)
		if srcMatched || tgtMatched || factMatched {
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

// PanoramaSearch performs a broad search using TF-IDF to find seed nodes, then
// expands via BFS multi-hop traversal. Results are ranked by BFS closeness score
// boosted by PageRank.
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

	// Build TF-IDF scorer and compute PageRank
	scorer := BuildTFIDF(allNodes)
	pageranks := ComputePageRank(allNodes, allEdges)

	// Find seed nodes via TF-IDF + substring match
	var seedIDs []string
	seedSet := make(map[string]bool)
	for _, n := range allNodes {
		tfidf := scorer(n.ID, query)
		subMatch := matchesAny(keywords, n.Name, n.Type, n.Summary)
		if tfidf > 0 || subMatch {
			if !seedSet[n.ID] {
				seedSet[n.ID] = true
				seedIDs = append(seedIDs, n.ID)
			}
		}
	}

	// BFS expansion from seed nodes (maxHops=2)
	bfsWeights := BFSNeighborhood(seedIDs, allEdges, 2)

	// Score all nodes that BFS touched
	type scoredNode struct {
		node  db.Node
		score float64
	}
	var scored []scoredNode
	for _, n := range allNodes {
		bfsW, inBFS := bfsWeights[n.ID]
		if !inBFS {
			continue
		}
		// TODO: use DB pagerank once schema upgraded
		pr := pageranks[n.ID]
		score := bfsW*0.7 + pr*0.3
		scored = append(scored, scoredNode{n, score})
	}

	// Sort by score DESC
	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })

	// Collect all BFS-touched nodes (used for edge collection before limit)
	allScoredIDs := make(map[string]bool, len(scored))
	for _, sn := range scored {
		allScoredIDs[sn.node.ID] = true
	}

	// Populate result respecting limit for nodes
	for _, sn := range scored {
		if limit > 0 && len(result.Nodes) >= limit {
			break
		}
		result.Nodes = append(result.Nodes, sn.node)
	}

	// Collect edges between any BFS-touched nodes
	finalEdges := make(map[string]db.Edge)
	for _, e := range allEdges {
		if allScoredIDs[e.SourceID] || allScoredIDs[e.TargetID] {
			finalEdges[e.ID] = e
		}
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

// GraphContext builds a formatted context string for use in LLM prompts.
// It's the ZEP-equivalent of get_simulation_context() for report generation.
// Returns top facts, entity summaries, and community context.
func GraphContext(database *db.DB, projectID, query string, maxFacts int) string {
	if maxFacts <= 0 {
		maxFacts = 20
	}

	result := PanoramaSearch(database, projectID, query, 30)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Graph Context for: %s\n\n", query))

	if len(result.Nodes) > 0 {
		sb.WriteString(fmt.Sprintf("### Key Entities (%d found)\n", len(result.Nodes)))
		for i, n := range result.Nodes {
			if i >= 10 {
				break
			}
			sb.WriteString(fmt.Sprintf("- **%s** (%s): %s\n", n.Name, n.Type, n.Summary))
		}
		sb.WriteString("\n")
	}

	if len(result.Facts) > 0 {
		sb.WriteString(fmt.Sprintf("### Key Facts (%d found)\n", len(result.Facts)))
		for i, f := range result.Facts {
			if i >= maxFacts {
				sb.WriteString(fmt.Sprintf("... and %d more facts\n", len(result.Facts)-maxFacts))
				break
			}
			sb.WriteString(fmt.Sprintf("- %s\n", f))
		}
		sb.WriteString("\n")
	}

	return sb.String()
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
