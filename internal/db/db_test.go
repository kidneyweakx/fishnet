package db

import (
	"testing"
)

// openMem opens an in-memory SQLite database for testing.
func openMem(t *testing.T) *DB {
	t.Helper()
	d, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// mustProject creates a project and returns its ID, failing the test on error.
func mustProject(t *testing.T, d *DB, name string) string {
	t.Helper()
	id, err := d.UpsertProject(name, ".")
	if err != nil {
		t.Fatalf("UpsertProject(%q): %v", name, err)
	}
	return id
}

// ─── Project ─────────────────────────────────────────────────────────────────

func TestDB_UpsertProject_CreateAndRetrieve(t *testing.T) {
	d := openMem(t)

	id, err := d.UpsertProject("myproject", "/tmp/myproject")
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty project ID")
	}

	got, err := d.ProjectByName("myproject")
	if err != nil {
		t.Fatalf("ProjectByName: %v", err)
	}
	if got != id {
		t.Errorf("ProjectByName = %q, want %q", got, id)
	}
}

func TestDB_UpsertProject_Idempotent(t *testing.T) {
	d := openMem(t)

	id1, err := d.UpsertProject("proj", "/tmp/a")
	if err != nil {
		t.Fatalf("first UpsertProject: %v", err)
	}
	id2, err := d.UpsertProject("proj", "/tmp/b")
	if err != nil {
		t.Fatalf("second UpsertProject: %v", err)
	}
	if id1 != id2 {
		t.Errorf("second upsert returned different ID: %q vs %q", id1, id2)
	}
}

func TestDB_ProjectByName_NotFound(t *testing.T) {
	d := openMem(t)
	_, err := d.ProjectByName("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing project, got nil")
	}
}

func TestDB_ListProjects(t *testing.T) {
	d := openMem(t)
	mustProject(t, d, "alpha")
	mustProject(t, d, "beta")

	projects, err := d.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 2 {
		t.Errorf("len(ListProjects) = %d, want 2", len(projects))
	}
}

// ─── Nodes ───────────────────────────────────────────────────────────────────

func TestDB_NodeUpsert_Deduplication(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")

	id1, err := d.UpsertNode(projID, "Alice", "Person", "researcher", "{}")
	if err != nil {
		t.Fatalf("first UpsertNode: %v", err)
	}
	id2, err := d.UpsertNode(projID, "Alice", "Person", "updated summary", "{}")
	if err != nil {
		t.Fatalf("second UpsertNode: %v", err)
	}
	if id1 != id2 {
		t.Errorf("second upsert returned different ID: %q vs %q", id1, id2)
	}

	nodes, err := d.GetNodes(projID)
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("len(GetNodes) = %d, want 1 (deduplicated)", len(nodes))
	}
}

func TestDB_NodeUpsert_SameNameDifferentType(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")

	_, err := d.UpsertNode(projID, "Alice", "Person", "a person", "{}")
	if err != nil {
		t.Fatalf("UpsertNode Person: %v", err)
	}
	_, err = d.UpsertNode(projID, "Alice", "Company", "a company", "{}")
	if err != nil {
		t.Fatalf("UpsertNode Company: %v", err)
	}

	nodes, err := d.GetNodes(projID)
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Errorf("len(GetNodes) = %d, want 2 (different types)", len(nodes))
	}
}

func TestDB_GetNodes_ProjectIsolation(t *testing.T) {
	d := openMem(t)
	p1 := mustProject(t, d, "projA")
	p2 := mustProject(t, d, "projB")

	d.UpsertNode(p1, "Alice", "Person", "", "{}")
	d.UpsertNode(p2, "Bob", "Person", "", "{}")

	nodesA, _ := d.GetNodes(p1)
	nodesB, _ := d.GetNodes(p2)

	if len(nodesA) != 1 {
		t.Errorf("projA nodes = %d, want 1", len(nodesA))
	}
	if len(nodesB) != 1 {
		t.Errorf("projB nodes = %d, want 1", len(nodesB))
	}
	if nodesA[0].Name == nodesB[0].Name {
		t.Errorf("project isolation broken: both have node name %q", nodesA[0].Name)
	}
}

func TestDB_UpdateCommunity(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")

	nodeID, err := d.UpsertNode(projID, "Alice", "Person", "", "{}")
	if err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}

	if err := d.UpdateCommunity(nodeID, 42); err != nil {
		t.Fatalf("UpdateCommunity: %v", err)
	}

	nodes, err := d.GetNodes(projID)
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least one node")
	}
	if nodes[0].CommunityID != 42 {
		t.Errorf("CommunityID = %d, want 42", nodes[0].CommunityID)
	}
}

// ─── Edges ───────────────────────────────────────────────────────────────────

func TestDB_UpsertEdge_Basic(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")

	srcID, _ := d.UpsertNode(projID, "Alice", "Person", "", "{}")
	tgtID, _ := d.UpsertNode(projID, "Acme Corp", "Company", "", "{}")

	err := d.UpsertEdge(projID, srcID, tgtID, "WORKS_FOR", "Alice works at Acme")
	if err != nil {
		t.Fatalf("UpsertEdge: %v", err)
	}

	edges, err := d.GetEdges(projID)
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("len(GetEdges) = %d, want 1", len(edges))
	}
	if edges[0].Type != "WORKS_FOR" {
		t.Errorf("edge type = %q, want %q", edges[0].Type, "WORKS_FOR")
	}
}

func TestDB_UpsertEdge_Deduplication(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")

	srcID, _ := d.UpsertNode(projID, "Alice", "Person", "", "{}")
	tgtID, _ := d.UpsertNode(projID, "Bob", "Person", "", "{}")

	d.UpsertEdge(projID, srcID, tgtID, "KNOWS", "first mention")
	d.UpsertEdge(projID, srcID, tgtID, "KNOWS", "second mention")

	edges, err := d.GetEdges(projID)
	if err != nil {
		t.Fatalf("GetEdges: %v", err)
	}
	if len(edges) != 1 {
		t.Errorf("expected 1 deduplicated edge, got %d", len(edges))
	}
	// Weight should have increased due to second upsert
	if edges[0].Weight <= 1.0 {
		t.Errorf("expected weight > 1.0 after duplicate upsert, got %f", edges[0].Weight)
	}
}

func TestDB_GetEdges_ProjectIsolation(t *testing.T) {
	d := openMem(t)
	p1 := mustProject(t, d, "projA")
	p2 := mustProject(t, d, "projB")

	a1, _ := d.UpsertNode(p1, "Alice", "Person", "", "{}")
	a2, _ := d.UpsertNode(p1, "Bob", "Person", "", "{}")
	b1, _ := d.UpsertNode(p2, "Charlie", "Person", "", "{}")
	b2, _ := d.UpsertNode(p2, "Dave", "Person", "", "{}")

	d.UpsertEdge(p1, a1, a2, "KNOWS", "")
	d.UpsertEdge(p2, b1, b2, "KNOWS", "")

	e1, _ := d.GetEdges(p1)
	e2, _ := d.GetEdges(p2)
	if len(e1) != 1 || len(e2) != 1 {
		t.Errorf("edge isolation: projA=%d, projB=%d, want 1 each", len(e1), len(e2))
	}
}

// ─── Stats ───────────────────────────────────────────────────────────────────

func TestDB_GetStats_CorrectCounts(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")

	n1, _ := d.UpsertNode(projID, "Alice", "Person", "", "{}")
	n2, _ := d.UpsertNode(projID, "Bob", "Person", "", "{}")
	d.UpsertEdge(projID, n1, n2, "KNOWS", "")

	stats := d.GetStats(projID)
	if stats.Nodes != 2 {
		t.Errorf("Stats.Nodes = %d, want 2", stats.Nodes)
	}
	if stats.Edges != 1 {
		t.Errorf("Stats.Edges = %d, want 1", stats.Edges)
	}
}

func TestDB_GetStats_Communities(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")

	n1, _ := d.UpsertNode(projID, "Alice", "Person", "", "{}")
	n2, _ := d.UpsertNode(projID, "Bob", "Person", "", "{}")
	d.UpdateCommunity(n1, 0)
	d.UpdateCommunity(n2, 1)

	stats := d.GetStats(projID)
	if stats.Communities != 2 {
		t.Errorf("Stats.Communities = %d, want 2", stats.Communities)
	}
}

func TestDB_GetStats_Empty(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "empty")

	stats := d.GetStats(projID)
	if stats.Nodes != 0 || stats.Edges != 0 {
		t.Errorf("expected empty stats, got %+v", stats)
	}
}

// ─── Simulations ─────────────────────────────────────────────────────────────

func TestDB_CreateAndFinishSim(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")

	simID, err := d.CreateSim(projID, "test scenario")
	if err != nil {
		t.Fatalf("CreateSim: %v", err)
	}
	if simID == "" {
		t.Fatal("expected non-empty sim ID")
	}

	if err := d.FinishSim(simID, "success"); err != nil {
		t.Fatalf("FinishSim: %v", err)
	}

	result, err := d.GetSimResult(simID)
	if err != nil {
		t.Fatalf("GetSimResult: %v", err)
	}
	if result != "success" {
		t.Errorf("GetSimResult = %q, want %q", result, "success")
	}
}

func TestDB_GetSimsByProject(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")

	d.CreateSim(projID, "scenario A")
	d.CreateSim(projID, "scenario B")

	sims, err := d.GetSimsByProject(projID, 10)
	if err != nil {
		t.Fatalf("GetSimsByProject: %v", err)
	}
	if len(sims) != 2 {
		t.Errorf("GetSimsByProject = %d, want 2", len(sims))
	}
}

// ─── Sim Posts ────────────────────────────────────────────────────────────────

func TestDB_SaveAndGetSimPost(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")
	simID, _ := d.CreateSim(projID, "test")

	err := d.SaveSimPost(simID, projID, "twitter", "agent1", "Alice", "Hello world", "", "", []string{"#test"}, 0, 0, 1)
	if err != nil {
		t.Fatalf("SaveSimPost: %v", err)
	}

	posts, err := d.GetSimPosts(simID, "", 10)
	if err != nil {
		t.Fatalf("GetSimPosts: %v", err)
	}
	if len(posts) != 1 {
		t.Errorf("GetSimPosts = %d, want 1", len(posts))
	}
	if posts[0].Content != "Hello world" {
		t.Errorf("post content = %q, want %q", posts[0].Content, "Hello world")
	}
	if len(posts[0].Tags) != 1 || posts[0].Tags[0] != "#test" {
		t.Errorf("post tags = %v, want [#test]", posts[0].Tags)
	}
}

func TestDB_GetSimPosts_PlatformFilter(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")
	simID, _ := d.CreateSim(projID, "test")

	d.SaveSimPost(simID, projID, "twitter", "a1", "Alice", "tweet", "", "", nil, 0, 0, 1)
	d.SaveSimPost(simID, projID, "reddit", "a2", "Bob", "reddit post", "", "r/news", nil, 0, 0, 1)

	twitterPosts, err := d.GetSimPosts(simID, "twitter", 10)
	if err != nil {
		t.Fatalf("GetSimPosts(twitter): %v", err)
	}
	if len(twitterPosts) != 1 {
		t.Errorf("twitter posts = %d, want 1", len(twitterPosts))
	}
}

// ─── Sim Actions ──────────────────────────────────────────────────────────────

func TestDB_SaveAndGetSimAction(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")
	simID, _ := d.CreateSim(projID, "test")

	err := d.SaveSimAction(simID, projID, "twitter", 1, "agent1", "Alice", "CREATE_POST", "", "my post content", true)
	if err != nil {
		t.Fatalf("SaveSimAction: %v", err)
	}

	actions, err := d.GetSimActions(simID, "", "", 10)
	if err != nil {
		t.Fatalf("GetSimActions: %v", err)
	}
	if len(actions) != 1 {
		t.Errorf("GetSimActions = %d, want 1", len(actions))
	}
	if actions[0].ActionType != "CREATE_POST" {
		t.Errorf("ActionType = %q, want %q", actions[0].ActionType, "CREATE_POST")
	}
	if !actions[0].Success {
		t.Error("expected Success = true")
	}
}

func TestDB_GetSimActions_AgentFilter(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")
	simID, _ := d.CreateSim(projID, "test")

	d.SaveSimAction(simID, projID, "twitter", 1, "agent1", "Alice", "CREATE_POST", "", "post", true)
	d.SaveSimAction(simID, projID, "twitter", 1, "agent2", "Bob", "LIKE_POST", "p1", "", true)

	agent1Actions, err := d.GetSimActions(simID, "agent1", "", 10)
	if err != nil {
		t.Fatalf("GetSimActions(agent1): %v", err)
	}
	if len(agent1Actions) != 1 {
		t.Errorf("agent1 actions = %d, want 1", len(agent1Actions))
	}
}

func TestDB_GetAgentStats(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")
	simID, _ := d.CreateSim(projID, "test")

	d.SaveSimAction(simID, projID, "twitter", 1, "agent1", "Alice", "CREATE_POST", "", "post1", true)
	d.SaveSimAction(simID, projID, "twitter", 2, "agent1", "Alice", "CREATE_POST", "", "post2", true)
	d.SaveSimAction(simID, projID, "twitter", 1, "agent1", "Alice", "LIKE_POST", "p1", "", true)

	stats, err := d.GetAgentStats(simID)
	if err != nil {
		t.Fatalf("GetAgentStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("agent stats count = %d, want 1", len(stats))
	}
	if stats[0].TotalPosts != 2 {
		t.Errorf("TotalPosts = %d, want 2", stats[0].TotalPosts)
	}
	if stats[0].TotalLikes != 1 {
		t.Errorf("TotalLikes = %d, want 1", stats[0].TotalLikes)
	}
}

// ─── Documents and Chunks ────────────────────────────────────────────────────

func TestDB_AddDocumentAndChunks(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")

	docID, err := d.AddDocument(projID, "/tmp/test.md", "test.md", "hello world", 2)
	if err != nil {
		t.Fatalf("AddDocument: %v", err)
	}

	if err := d.AddChunk(docID, projID, "hello", 0); err != nil {
		t.Fatalf("AddChunk 0: %v", err)
	}
	if err := d.AddChunk(docID, projID, "world", 1); err != nil {
		t.Fatalf("AddChunk 1: %v", err)
	}

	chunks, err := d.UnprocessedChunks(projID)
	if err != nil {
		t.Fatalf("UnprocessedChunks: %v", err)
	}
	if len(chunks) != 2 {
		t.Errorf("UnprocessedChunks = %d, want 2", len(chunks))
	}
}

func TestDB_MarkChunkDone(t *testing.T) {
	d := openMem(t)
	projID := mustProject(t, d, "proj1")

	docID, _ := d.AddDocument(projID, "/tmp/test.md", "test.md", "content", 1)
	d.AddChunk(docID, projID, "chunk content", 0)

	chunks, _ := d.UnprocessedChunks(projID)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 unprocessed chunk, got %d", len(chunks))
	}

	if err := d.MarkChunkDone(chunks[0].ID); err != nil {
		t.Fatalf("MarkChunkDone: %v", err)
	}

	remaining, _ := d.UnprocessedChunks(projID)
	if len(remaining) != 0 {
		t.Errorf("expected 0 unprocessed chunks after mark done, got %d", len(remaining))
	}
}
