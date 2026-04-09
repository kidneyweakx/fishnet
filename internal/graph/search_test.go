package graph

import (
	"context"
	"testing"

	"fishnet/internal/config"
	"fishnet/internal/db"
	"fishnet/internal/llm"
)

// stubLLM returns a noop LLM client (no real API calls).
func stubLLM() *llm.Client {
	return llm.New(config.LLMConfig{Provider: "none"})
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func searchTestDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	projID, err := database.UpsertProject("p1", ".")
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	return database, projID
}

// ─── tokenize ────────────────────────────────────────────────────────────────

func TestTokenize_Basic(t *testing.T) {
	tokens := tokenize("Alice Bob")
	if len(tokens) != 2 {
		t.Errorf("tokenize = %v, want 2 tokens", tokens)
	}
}

func TestTokenize_CaseLower(t *testing.T) {
	tokens := tokenize("ALICE")
	if len(tokens) != 1 || tokens[0] != "alice" {
		t.Errorf("tokenize uppercase = %v, want [alice]", tokens)
	}
}

func TestTokenize_FiltersShortWords(t *testing.T) {
	// single-letter words should be filtered
	tokens := tokenize("a b cc ddd")
	for _, tok := range tokens {
		if len(tok) < 2 {
			t.Errorf("tokenize returned short token %q", tok)
		}
	}
}

func TestTokenize_EmptyQuery(t *testing.T) {
	tokens := tokenize("")
	if len(tokens) != 0 {
		t.Errorf("tokenize empty = %v, want []", tokens)
	}
}

// ─── matchesAny ──────────────────────────────────────────────────────────────

func TestMatchesAny_Hit(t *testing.T) {
	if !matchesAny([]string{"alice"}, "Alice Chen", "Person") {
		t.Error("matchesAny should match 'alice' in 'Alice Chen'")
	}
}

func TestMatchesAny_Miss(t *testing.T) {
	if matchesAny([]string{"bob"}, "Alice Chen", "Person") {
		t.Error("matchesAny should not match 'bob' in 'Alice Chen'")
	}
}

func TestMatchesAny_CaseInsensitive(t *testing.T) {
	if !matchesAny([]string{"alice"}, "ALICE") {
		t.Error("matchesAny should be case-insensitive")
	}
}

func TestMatchesAny_MultipleKeywords(t *testing.T) {
	// At least one keyword must match
	if !matchesAny([]string{"xyz", "alice"}, "Alice Chen") {
		t.Error("matchesAny should return true if any keyword matches")
	}
}

// ─── QuickSearch ─────────────────────────────────────────────────────────────

func TestQuickSearch_EmptyQuery(t *testing.T) {
	db, projID := searchTestDB(t)
	result := QuickSearch(db, projID, "", 10)
	if len(result.Nodes) != 0 {
		t.Errorf("empty query should return no nodes, got %d", len(result.Nodes))
	}
}

func TestQuickSearch_FindsByName(t *testing.T) {
	database, projID := searchTestDB(t)

	database.UpsertNode(projID, "Alice Chen", "Person", "A researcher", "{}")
	database.UpsertNode(projID, "Bob Smith", "Company", "Tech startup", "{}")

	result := QuickSearch(database, projID, "alice", 10)
	if len(result.Nodes) != 1 {
		t.Errorf("QuickSearch('alice') nodes = %d, want 1", len(result.Nodes))
	}
	if len(result.Nodes) > 0 && result.Nodes[0].Name != "Alice Chen" {
		t.Errorf("QuickSearch returned %q, want 'Alice Chen'", result.Nodes[0].Name)
	}
}

func TestQuickSearch_FindsByType(t *testing.T) {
	database, projID := searchTestDB(t)

	database.UpsertNode(projID, "Alice", "Person", "", "{}")
	database.UpsertNode(projID, "Acme Corp", "Company", "", "{}")

	result := QuickSearch(database, projID, "company", 10)
	if len(result.Nodes) != 1 {
		t.Errorf("QuickSearch('company') nodes = %d, want 1", len(result.Nodes))
	}
}

func TestQuickSearch_FindsBySummary(t *testing.T) {
	database, projID := searchTestDB(t)

	database.UpsertNode(projID, "Alice", "Person", "climate scientist", "{}")
	database.UpsertNode(projID, "Bob", "Person", "software engineer", "{}")

	result := QuickSearch(database, projID, "climate", 10)
	if len(result.Nodes) != 1 {
		t.Errorf("QuickSearch('climate') nodes = %d, want 1", len(result.Nodes))
	}
}

func TestQuickSearch_LimitRespected(t *testing.T) {
	database, projID := searchTestDB(t)

	// Insert 5 "person" nodes that all match the query "person"
	for i := 0; i < 5; i++ {
		database.UpsertNode(projID, "Person"+string(rune('A'+i)), "Person", "", "{}")
	}

	result := QuickSearch(database, projID, "person", 3)
	if len(result.Nodes) > 3 {
		t.Errorf("QuickSearch with limit=3 returned %d nodes", len(result.Nodes))
	}
}

func TestQuickSearch_QueryStoredInResult(t *testing.T) {
	database, projID := searchTestDB(t)
	result := QuickSearch(database, projID, "myquery", 10)
	if result.Query != "myquery" {
		t.Errorf("result.Query = %q, want %q", result.Query, "myquery")
	}
}

func TestQuickSearch_FindsEdgesByFact(t *testing.T) {
	database, projID := searchTestDB(t)

	n1, _ := database.UpsertNode(projID, "Alice", "Person", "", "{}")
	n2, _ := database.UpsertNode(projID, "Acme", "Company", "", "{}")
	database.UpsertEdge(projID, n1, n2, "WORKS_FOR", "Alice is the lead engineer at Acme")

	// Query on fact text — neither node name contains "engineer"
	result := QuickSearch(database, projID, "engineer", 10)
	if len(result.Edges) == 0 {
		t.Error("expected at least one edge matching fact 'engineer'")
	}
}

func TestQuickSearch_IncludesEdgeFacts(t *testing.T) {
	database, projID := searchTestDB(t)

	n1, _ := database.UpsertNode(projID, "Alice", "Person", "", "{}")
	n2, _ := database.UpsertNode(projID, "Bob", "Person", "", "{}")
	database.UpsertEdge(projID, n1, n2, "KNOWS", "Alice introduced Bob to the team")

	result := QuickSearch(database, projID, "alice", 10)
	if len(result.Facts) == 0 {
		t.Error("expected at least one fact in results")
	}
}

func TestQuickSearch_NoMatch(t *testing.T) {
	database, projID := searchTestDB(t)
	database.UpsertNode(projID, "Alice", "Person", "", "{}")

	result := QuickSearch(database, projID, "xyznotfound", 10)
	if len(result.Nodes) != 0 {
		t.Errorf("expected no results for 'xyznotfound', got %d nodes", len(result.Nodes))
	}
}

// ─── PanoramaSearch ───────────────────────────────────────────────────────────

func TestPanoramaSearch_EmptyQuery(t *testing.T) {
	database, projID := searchTestDB(t)
	result := PanoramaSearch(database, projID, "", 10)
	if len(result.Nodes) != 0 {
		t.Errorf("empty query should return no nodes, got %d", len(result.Nodes))
	}
}

func TestPanoramaSearch_IncludesNeighbors(t *testing.T) {
	database, projID := searchTestDB(t)

	// Alice -- KNOWS --> Bob -- KNOWS --> Charlie
	n1, _ := database.UpsertNode(projID, "Alice", "Person", "", "{}")
	n2, _ := database.UpsertNode(projID, "Bob", "Person", "", "{}")
	n3, _ := database.UpsertNode(projID, "Charlie", "Person", "", "{}")
	database.UpsertEdge(projID, n1, n2, "KNOWS", "")
	database.UpsertEdge(projID, n2, n3, "KNOWS", "")

	// Search for Alice — should also return Bob (neighbor)
	result := PanoramaSearch(database, projID, "alice", 20)
	nodeNames := make(map[string]bool)
	for _, n := range result.Nodes {
		nodeNames[n.Name] = true
	}
	if !nodeNames["Alice"] {
		t.Error("PanoramaSearch('alice') should include Alice")
	}
	if !nodeNames["Bob"] {
		t.Error("PanoramaSearch('alice') should include neighbor Bob")
	}
}

func TestPanoramaSearch_ReturnsEdges(t *testing.T) {
	database, projID := searchTestDB(t)

	n1, _ := database.UpsertNode(projID, "Alice", "Person", "", "{}")
	n2, _ := database.UpsertNode(projID, "Bob", "Person", "", "{}")
	database.UpsertEdge(projID, n1, n2, "KNOWS", "they work together")

	result := PanoramaSearch(database, projID, "alice", 10)
	if len(result.Edges) == 0 {
		t.Error("PanoramaSearch should return edges connected to matched nodes")
	}
}

// ─── tokenize edge cases ──────────────────────────────────────────────────────

func TestTokenize_WhitespaceOnly(t *testing.T) {
	tokens := tokenize("   ")
	if len(tokens) != 0 {
		t.Errorf("whitespace-only query should give no tokens, got %v", tokens)
	}
}

func TestTokenize_MultipleSpaces(t *testing.T) {
	tokens := tokenize("alice   bob")
	if len(tokens) != 2 {
		t.Errorf("expected 2 tokens, got %v", tokens)
	}
}

// ─── InsightForge ─────────────────────────────────────────────────────────────

func TestInsightForge_FallbackOnLLMError(t *testing.T) {
	// With a stub LLM (no API), InsightForge should fall back to a plain
	// QuickSearch on the original query and return a non-nil result.
	database, projID := searchTestDB(t)
	database.UpsertNode(projID, "Alice Chen", "Person", "climate scientist", "{}")
	database.UpsertNode(projID, "Bob Smith", "Company", "tech startup", "{}")

	result, err := InsightForge(context.Background(), database, stubLLM(), projID, "alice")
	if err != nil {
		t.Fatalf("InsightForge returned error: %v", err)
	}
	if result.Query != "alice" {
		t.Errorf("result.Query = %q, want %q", result.Query, "alice")
	}
	// Fallback must still find Alice.
	found := false
	for _, n := range result.Nodes {
		if n.Name == "Alice Chen" {
			found = true
			break
		}
	}
	if !found {
		t.Error("InsightForge fallback should return Alice Chen via QuickSearch")
	}
}

func TestInsightForge_DeduplicatesNodes(t *testing.T) {
	// Even if multiple sub-questions would match the same node, it should
	// appear only once in the merged result.
	database, projID := searchTestDB(t)
	database.UpsertNode(projID, "Alice", "Person", "the main character", "{}")

	result, err := InsightForge(context.Background(), database, stubLLM(), projID, "alice main character")
	if err != nil {
		t.Fatalf("InsightForge error: %v", err)
	}
	seen := make(map[string]int)
	for _, n := range result.Nodes {
		seen[n.ID]++
	}
	for id, count := range seen {
		if count > 1 {
			t.Errorf("node %s appears %d times in InsightForge result (want 1)", id, count)
		}
	}
}
