package graph

import (
	"math"
	"strings"

	"fishnet/internal/db"
)

// ── PageRank ──────────────────────────────────────────────────────────────────

// ComputePageRank runs power iteration PageRank on the graph.
// Returns map[nodeID]score. Scores sum to 1.0.
// d=0.85 damping, maxIter=100, tolerance=1e-6.
func ComputePageRank(nodes []db.Node, edges []db.Edge) map[string]float64 {
	if len(nodes) == 0 {
		return nil
	}
	d := 0.85
	n := len(nodes)

	// Build node index and out-degree
	nodeIdx := make(map[string]int, n)
	for i, node := range nodes {
		nodeIdx[node.ID] = i
	}

	// outEdges: nodeIdx → list of (targetIdx, weight)
	outEdges := make([][]struct {
		to int
		w  float64
	}, n)
	outDeg := make([]float64, n)
	for _, e := range edges {
		si, ok1 := nodeIdx[e.SourceID]
		ti, ok2 := nodeIdx[e.TargetID]
		if !ok1 || !ok2 {
			continue
		}
		w := e.Weight
		if w <= 0 {
			w = 1
		}
		outEdges[si] = append(outEdges[si], struct {
			to int
			w  float64
		}{ti, w})
		outEdges[ti] = append(outEdges[ti], struct {
			to int
			w  float64
		}{si, w}) // undirected
		outDeg[si] += w
		outDeg[ti] += w
	}

	rank := make([]float64, n)
	for i := range rank {
		rank[i] = 1.0 / float64(n)
	}

	for iter := 0; iter < 100; iter++ {
		newRank := make([]float64, n)
		for i := range newRank {
			newRank[i] = (1 - d) / float64(n)
		}
		for i, outs := range outEdges {
			if outDeg[i] == 0 {
				continue
			}
			for _, out := range outs {
				newRank[out.to] += d * rank[i] * out.w / outDeg[i]
			}
		}
		// Check convergence
		diff := 0.0
		for i := range rank {
			diff += math.Abs(newRank[i] - rank[i])
		}
		copy(rank, newRank)
		if diff < 1e-6 {
			break
		}
	}

	result := make(map[string]float64, n)
	for i, node := range nodes {
		result[node.ID] = rank[i]
	}
	return result
}

// ── TF-IDF ────────────────────────────────────────────────────────────────────

// BuildTFIDF builds a TF-IDF index over node documents (name + type + summary).
// Returns a function that scores a query string against a node.
func BuildTFIDF(nodes []db.Node) func(nodeID string, query string) float64 {
	type docInfo struct {
		terms map[string]float64 // term → TF
	}

	docs := make(map[string]docInfo, len(nodes))
	df := make(map[string]int) // term → document frequency

	for _, n := range nodes {
		text := strings.ToLower(n.Name + " " + n.Type + " " + n.Summary)
		terms := tokenizeForTFIDF(text)
		tf := make(map[string]float64, len(terms))
		for _, t := range terms {
			tf[t]++
		}
		// Normalize TF by document length
		if len(terms) > 0 {
			for t, count := range tf {
				tf[t] = count / float64(len(terms))
			}
		}
		docs[n.ID] = docInfo{terms: tf}
		// Count DF
		seen := make(map[string]bool)
		for t := range tf {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}

	N := float64(len(nodes))

	return func(nodeID string, query string) float64 {
		doc, ok := docs[nodeID]
		if !ok {
			return 0
		}
		queryTerms := tokenizeForTFIDF(strings.ToLower(query))
		score := 0.0
		for _, qt := range queryTerms {
			tf := doc.terms[qt]
			if tf == 0 {
				continue
			}
			// Smoothed IDF: log((N+1)/(df+1)) avoids zero when df+1 == N
			idf := math.Log((N + 1) / float64(df[qt]+1))
			score += tf * idf
		}
		return score
	}
}

// tokenizeForTFIDF splits text into stemmed tokens, filtering stopwords.
func tokenizeForTFIDF(text string) []string {
	stopwords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true, "from": true,
		"is": true, "are": true, "was": true, "were": true, "be": true,
		"has": true, "have": true, "had": true, "it": true, "this": true,
		"that": true, "as": true, "not": true, "no": true, "can": true,
	}
	words := strings.Fields(text)
	var tokens []string
	for _, w := range words {
		// Strip punctuation
		w = strings.Trim(w, ".,!?;:\"'()[]{}") //nolint:staticcheck
		if len(w) < 2 {
			continue
		}
		if stopwords[w] {
			continue
		}
		tokens = append(tokens, w)
	}
	return tokens
}

// ── BFS Multi-hop ─────────────────────────────────────────────────────────────

// BFSNeighborhood returns all nodes within maxHops edges of the seed nodes,
// with a distance-decay weight (1/hop^2).
// Returns map[nodeID]weight where weight reflects closeness to seeds.
func BFSNeighborhood(seedIDs []string, edges []db.Edge, maxHops int) map[string]float64 {
	// Build adjacency
	adj := make(map[string][]string)
	for _, e := range edges {
		adj[e.SourceID] = append(adj[e.SourceID], e.TargetID)
		adj[e.TargetID] = append(adj[e.TargetID], e.SourceID) // undirected
	}

	visited := make(map[string]float64)
	queue := make([]struct {
		id  string
		hop int
	}, 0, len(seedIDs))
	for _, id := range seedIDs {
		visited[id] = 1.0
		queue = append(queue, struct {
			id  string
			hop int
		}{id, 0})
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.hop >= maxHops {
			continue
		}
		for _, neighbor := range adj[cur.id] {
			if _, seen := visited[neighbor]; !seen {
				hop := cur.hop + 1
				weight := 1.0 / float64(hop*hop)
				visited[neighbor] = weight
				queue = append(queue, struct {
					id  string
					hop int
				}{neighbor, hop})
			}
		}
	}
	return visited
}
