package graph

import (
	"context"
	"fmt"
	"strings"

	"fishnet/internal/db"
	"fishnet/internal/llm"
)

// ─── Louvain Community Detection ─────────────────────────────────────────────
// Simplified greedy Louvain: one-pass modularity optimization.

type graphData struct {
	nodes   []db.Node
	edges   []db.Edge
	nodeIdx map[string]int // nodeID -> index
	adj     map[int][]adjEntry
	m       float64 // total edge weight
}

type adjEntry struct {
	to     int
	weight float64
}

func buildGraphData(nodes []db.Node, edges []db.Edge) *graphData {
	g := &graphData{
		nodes:   nodes,
		edges:   edges,
		nodeIdx: make(map[string]int),
		adj:     make(map[int][]adjEntry),
	}
	for i, n := range nodes {
		g.nodeIdx[n.ID] = i
	}
	for _, e := range edges {
		si, ok1 := g.nodeIdx[e.SourceID]
		ti, ok2 := g.nodeIdx[e.TargetID]
		if !ok1 || !ok2 {
			continue
		}
		w := e.Weight
		if w <= 0 {
			w = 1
		}
		g.adj[si] = append(g.adj[si], adjEntry{ti, w})
		g.adj[ti] = append(g.adj[ti], adjEntry{si, w})
		g.m += w
	}
	return g
}

func nodeDegree(g *graphData, i int) float64 {
	var d float64
	for _, a := range g.adj[i] {
		d += a.weight
	}
	return d
}

// louvain runs one phase and returns community assignments (nodeIndex -> communityID).
func louvain(g *graphData) map[int]int {
	n := len(g.nodes)
	comm := make(map[int]int, n)
	for i := range g.nodes {
		comm[i] = i
	}
	if g.m == 0 {
		return comm
	}

	improved := true
	const maxIter = 100
	for iter := 0; iter < maxIter && improved; iter++ {
		improved = false
		for node := 0; node < n; node++ {
			ki := nodeDegree(g, node)
			currentComm := comm[node]
			bestComm := currentComm
			bestGain := 0.0

			neighborWeight := make(map[int]float64)
			for _, a := range g.adj[node] {
				neighborWeight[comm[a.to]] += a.weight
			}

			commDegree := make(map[int]float64)
			for i, c := range comm {
				commDegree[c] += nodeDegree(g, i)
			}

			for c, wc := range neighborWeight {
				if c == currentComm {
					continue
				}
				gain := wc/g.m - (commDegree[c]*ki)/(2*g.m*g.m)
				leaveCost := neighborWeight[currentComm]/g.m - (commDegree[currentComm]*ki)/(2*g.m*g.m)
				if gain-leaveCost > bestGain {
					bestGain = gain - leaveCost
					bestComm = c
				}
			}

			if bestComm != currentComm {
				comm[node] = bestComm
				improved = true
			}
		}
	}
	return comm
}

func normalizeComms(comm map[int]int) map[int]int {
	seen := map[int]int{}
	next := 0
	out := make(map[int]int, len(comm))
	for node, c := range comm {
		if _, ok := seen[c]; !ok {
			seen[c] = next
			next++
		}
		out[node] = seen[c]
	}
	return out
}

// ─── Public API ───────────────────────────────────────────────────────────────

type CommunityResult struct {
	ID      int
	Nodes   []db.Node
	Summary string
}

// RunCommunityDetection detects communities, persists them, and optionally generates summaries.
func RunCommunityDetection(
	ctx context.Context,
	database *db.DB,
	client *llm.Client, // nil = skip LLM summaries
	projectID string,
	minSize int,
) ([]CommunityResult, error) {

	nodes, err := database.GetNodes(projectID)
	if err != nil {
		return nil, fmt.Errorf("get nodes: %w", err)
	}
	edges, err := database.GetEdges(projectID)
	if err != nil {
		return nil, fmt.Errorf("get edges: %w", err)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes found; run: fishnet analyze first")
	}

	g := buildGraphData(nodes, edges)
	comm := normalizeComms(louvain(g))

	commNodes := make(map[int][]db.Node)
	for idx, cid := range comm {
		commNodes[cid] = append(commNodes[cid], nodes[idx])
	}

	var results []CommunityResult
	for cid, members := range commNodes {
		if len(members) < minSize {
			// Still update DB with community ID, even for small ones
			for _, n := range members {
				_ = database.UpdateCommunity(n.ID, cid)
			}
			continue
		}
		for _, n := range members {
			_ = database.UpdateCommunity(n.ID, cid)
		}

		summary := fmt.Sprintf("Community of %d nodes", len(members))
		if client != nil {
			if s, err := summarizeCommunity(ctx, client, members); err == nil {
				summary = s
			}
		}
		results = append(results, CommunityResult{ID: cid, Nodes: members, Summary: summary})
	}
	return results, nil
}

func summarizeCommunity(ctx context.Context, client *llm.Client, nodes []db.Node) (string, error) {
	var sb strings.Builder
	for i, n := range nodes {
		if i >= 15 {
			fmt.Fprintf(&sb, "... and %d more\n", len(nodes)-i)
			break
		}
		fmt.Fprintf(&sb, "- %s (%s): %s\n", n.Name, n.Type, n.Summary)
	}
	return client.System(ctx,
		"You are a graph analyst. In one sentence, summarize what this cluster of entities has in common.",
		sb.String())
}
