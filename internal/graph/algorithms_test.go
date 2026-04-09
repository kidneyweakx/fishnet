package graph

import (
	"math"
	"testing"

	"fishnet/internal/db"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func makeNode(id, name, nodeType, summary string) db.Node {
	return db.Node{ID: id, Name: name, Type: nodeType, Summary: summary}
}

func makeEdge(id, src, tgt string, weight float64) db.Edge {
	return db.Edge{ID: id, SourceID: src, TargetID: tgt, Weight: weight}
}

// ─── PageRank ─────────────────────────────────────────────────────────────────

func TestPageRank_Convergence(t *testing.T) {
	nodes := []db.Node{
		makeNode("a", "A", "t", ""),
		makeNode("b", "B", "t", ""),
		makeNode("c", "C", "t", ""),
	}
	edges := []db.Edge{
		makeEdge("e1", "a", "b", 1),
		makeEdge("e2", "b", "c", 1),
	}
	pr := ComputePageRank(nodes, edges)
	if pr == nil {
		t.Fatal("ComputePageRank returned nil for a non-empty graph")
	}
	// Scores should sum to 1.0 (within floating-point tolerance)
	sum := 0.0
	for _, score := range pr {
		sum += score
	}
	if math.Abs(sum-1.0) > 1e-4 {
		t.Errorf("PageRank scores sum to %.6f, want ~1.0", sum)
	}
}

func TestPageRank_Empty(t *testing.T) {
	pr := ComputePageRank(nil, nil)
	if pr != nil {
		t.Errorf("ComputePageRank(nil, nil) = %v, want nil", pr)
	}
}

func TestPageRank_HighDegree(t *testing.T) {
	// Hub node "hub" is connected to three leaf nodes.
	// Hub should receive higher PageRank than any single leaf.
	nodes := []db.Node{
		makeNode("hub", "Hub", "t", ""),
		makeNode("l1", "Leaf1", "t", ""),
		makeNode("l2", "Leaf2", "t", ""),
		makeNode("l3", "Leaf3", "t", ""),
	}
	edges := []db.Edge{
		makeEdge("e1", "hub", "l1", 1),
		makeEdge("e2", "hub", "l2", 1),
		makeEdge("e3", "hub", "l3", 1),
	}
	pr := ComputePageRank(nodes, edges)
	hubScore := pr["hub"]
	for _, leaf := range []string{"l1", "l2", "l3"} {
		if hubScore <= pr[leaf] {
			t.Errorf("hub PageRank (%.4f) should be higher than %s PageRank (%.4f)", hubScore, leaf, pr[leaf])
		}
	}
}

func TestPageRank_AllScores(t *testing.T) {
	nodes := []db.Node{makeNode("x", "X", "t", "")}
	pr := ComputePageRank(nodes, nil)
	if pr["x"] <= 0 {
		t.Errorf("single-node PageRank should be positive, got %v", pr["x"])
	}
}

// ─── BFSNeighborhood ─────────────────────────────────────────────────────────

func TestBFSNeighborhood_Hops(t *testing.T) {
	// Chain: a -> b -> c -> d
	edges := []db.Edge{
		makeEdge("e1", "a", "b", 1),
		makeEdge("e2", "b", "c", 1),
		makeEdge("e3", "c", "d", 1),
	}

	// maxHops=1: only a and b reachable from seed "a"
	w1 := BFSNeighborhood([]string{"a"}, edges, 1)
	if _, ok := w1["a"]; !ok {
		t.Error("seed 'a' should be in BFS result with maxHops=1")
	}
	if _, ok := w1["b"]; !ok {
		t.Error("'b' at hop 1 should be in BFS result with maxHops=1")
	}
	if _, ok := w1["c"]; ok {
		t.Error("'c' at hop 2 should NOT be in BFS result with maxHops=1")
	}

	// maxHops=2: a, b, c reachable, d not
	w2 := BFSNeighborhood([]string{"a"}, edges, 2)
	if _, ok := w2["c"]; !ok {
		t.Error("'c' at hop 2 should be in BFS result with maxHops=2")
	}
	if _, ok := w2["d"]; ok {
		t.Error("'d' at hop 3 should NOT be in BFS result with maxHops=2")
	}
}

func TestBFSNeighborhood_Decay(t *testing.T) {
	// Chain: seed -> hop1 -> hop2
	edges := []db.Edge{
		makeEdge("e1", "seed", "hop1", 1),
		makeEdge("e2", "hop1", "hop2", 1),
	}
	w := BFSNeighborhood([]string{"seed"}, edges, 3)

	seedW := w["seed"]   // 1.0
	hop1W := w["hop1"]   // 1/(1^2) = 1.0
	hop2W := w["hop2"]   // 1/(2^2) = 0.25

	if seedW != 1.0 {
		t.Errorf("seed weight = %.4f, want 1.0", seedW)
	}
	if hop1W != 1.0 {
		t.Errorf("hop1 weight = %.4f, want 1.0", hop1W)
	}
	if hop2W != 0.25 {
		t.Errorf("hop2 weight = %.4f, want 0.25", hop2W)
	}
	// Closer nodes should have higher or equal weight
	if hop2W >= hop1W {
		t.Errorf("hop2 (%.4f) should be less than hop1 (%.4f) due to distance decay", hop2W, hop1W)
	}
}

func TestBFSNeighborhood_EmptySeeds(t *testing.T) {
	edges := []db.Edge{makeEdge("e1", "a", "b", 1)}
	w := BFSNeighborhood(nil, edges, 2)
	if len(w) != 0 {
		t.Errorf("empty seeds should produce empty result, got %v", w)
	}
}

func TestBFSNeighborhood_MultipleSeeds(t *testing.T) {
	// Two separate chains; seed both roots
	edges := []db.Edge{
		makeEdge("e1", "a", "b", 1),
		makeEdge("e2", "c", "d", 1),
	}
	w := BFSNeighborhood([]string{"a", "c"}, edges, 1)
	for _, id := range []string{"a", "b", "c", "d"} {
		if _, ok := w[id]; !ok {
			t.Errorf("node %q should be reachable from dual seeds", id)
		}
	}
}

// ─── BuildTFIDF ───────────────────────────────────────────────────────────────

func TestBuildTFIDF_Scoring(t *testing.T) {
	nodes := []db.Node{
		makeNode("n1", "Alice Smith", "Person", "climate researcher"),
		makeNode("n2", "Bob Jones", "Company", "software startup"),
	}
	scorer := BuildTFIDF(nodes)

	// "alice" query — node n1 has Alice in name
	aliceScore := scorer("n1", "alice")
	bobScore := scorer("n2", "alice")
	if aliceScore <= bobScore {
		t.Errorf("node with 'Alice' in name should score higher than one without: alice=%.4f bob=%.4f", aliceScore, bobScore)
	}
}

func TestBuildTFIDF_Stopwords(t *testing.T) {
	nodes := []db.Node{
		makeNode("n1", "The Company", "Organization", "is a company that has many people"),
	}
	scorer := BuildTFIDF(nodes)

	// Common stopwords like "the", "is", "has" should not contribute to score
	// Score for stopword-only query should be 0
	score := scorer("n1", "the is has")
	if score != 0 {
		t.Errorf("stopword-only query should score 0, got %.4f", score)
	}
}

func TestBuildTFIDF_UnknownNode(t *testing.T) {
	nodes := []db.Node{makeNode("n1", "Alice", "Person", "")}
	scorer := BuildTFIDF(nodes)
	score := scorer("nonexistent", "alice")
	if score != 0 {
		t.Errorf("unknown node should score 0, got %.4f", score)
	}
}

func TestBuildTFIDF_EmptyNodes(t *testing.T) {
	scorer := BuildTFIDF(nil)
	score := scorer("n1", "alice")
	if score != 0 {
		t.Errorf("empty node set should always score 0, got %.4f", score)
	}
}

func TestBuildTFIDF_RareTermScoresHigher(t *testing.T) {
	// "climate" appears in only one node's summary; "researcher" also rare.
	// "person" appears in all nodes' type fields.
	nodes := []db.Node{
		makeNode("n1", "Alice", "Person", "climate researcher"),
		makeNode("n2", "Bob", "Person", "software engineer"),
		makeNode("n3", "Carol", "Person", "finance analyst"),
	}
	scorer := BuildTFIDF(nodes)

	// n1 should score higher on "climate" (rare) than on "person" (common across all)
	climateScore := scorer("n1", "climate")
	// "person" appears in all 3 docs → IDF is very low
	personScore := scorer("n1", "person")
	if climateScore <= personScore {
		t.Errorf("rare term 'climate' (%.4f) should score higher than common term 'person' (%.4f)", climateScore, personScore)
	}
}
