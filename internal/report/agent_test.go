package report

import (
	"context"
	"sort"
	"testing"

	"fishnet/internal/db"
	"fishnet/internal/llm"
	"fishnet/internal/config"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func reportTestDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	projID, err := database.UpsertProject("rp1", ".")
	if err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	return database, projID
}

// stubLLM returns a noop LLM client (no API calls).
func stubLLM() *llm.Client {
	return llm.New(config.LLMConfig{Provider: "none"})
}

// ─── InterviewBatch ──────────────────────────────────────────────────────────

func TestInterviewBatch_EmptyNames(t *testing.T) {
	database, projID := reportTestDB(t)
	agent := New(database, stubLLM())

	result, err := agent.InterviewBatch(context.Background(), projID, nil, "hello?")
	if err != nil {
		t.Fatalf("InterviewBatch with nil names: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestInterviewBatch_UnknownAgent(t *testing.T) {
	database, projID := reportTestDB(t)
	agent := New(database, stubLLM())

	result, err := agent.InterviewBatch(context.Background(), projID, []string{"Nobody"}, "hello?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	resp, ok := result["Nobody"]
	if !ok {
		t.Fatal("expected key 'Nobody' in result")
	}
	if resp == "" {
		t.Error("expected non-empty error message for unknown agent")
	}
}

func TestInterviewBatch_ReturnsAllKeys(t *testing.T) {
	database, projID := reportTestDB(t)
	database.UpsertNode(projID, "Alice", "Person", "a researcher", "{}")
	database.UpsertNode(projID, "Bob", "Person", "a journalist", "{}")

	agent := New(database, stubLLM())
	names := []string{"Alice", "Bob", "Charlie"}

	result, err := agent.InterviewBatch(context.Background(), projID, names, "What do you think?")
	if err != nil {
		t.Fatalf("InterviewBatch: %v", err)
	}
	if len(result) != len(names) {
		t.Errorf("expected %d keys, got %d: %v", len(names), len(result), result)
	}

	// Verify all requested names appear as keys.
	got := make([]string, 0, len(result))
	for k := range result {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"Alice", "Bob", "Charlie"}
	sort.Strings(want)
	for i, w := range want {
		if got[i] != w {
			t.Errorf("key[%d]: got %q, want %q", i, got[i], w)
		}
	}
}

func TestInterviewBatch_CaseInsensitiveLookup(t *testing.T) {
	database, projID := reportTestDB(t)
	database.UpsertNode(projID, "Alice Chen", "Person", "researcher", "{}")

	agent := New(database, stubLLM())

	// Lookup with different casing.
	result, err := agent.InterviewBatch(context.Background(), projID, []string{"alice chen"}, "hello?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}
	// The response may be an LLM error (no real API), but the key must exist.
	if _, ok := result["alice chen"]; !ok {
		t.Error("expected key 'alice chen' in result map")
	}
}

func TestInterviewBatch_Concurrency(t *testing.T) {
	// Smoke-test that concurrent calls don't race. Run with -race.
	database, projID := reportTestDB(t)
	for _, name := range []string{"A1", "A2", "A3", "A4", "A5"} {
		database.UpsertNode(projID, name, "Person", "agent", "{}")
	}

	agent := New(database, stubLLM())
	names := []string{"A1", "A2", "A3", "A4", "A5"}

	result, err := agent.InterviewBatch(context.Background(), projID, names, "Question?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(names) {
		t.Errorf("expected %d entries, got %d", len(names), len(result))
	}
}

// ─── InterviewResult / InterviewReport struct tests ──────────────────────────

func TestInterviewResult_Fields(t *testing.T) {
	r := InterviewResult{
		AgentName: "Alice",
		Response:  "I think the situation is critical.",
		KeyQuotes: []string{"situation is critical", "I think"},
	}
	if r.AgentName != "Alice" {
		t.Errorf("AgentName: got %q, want %q", r.AgentName, "Alice")
	}
	if r.Response != "I think the situation is critical." {
		t.Errorf("Response mismatch")
	}
	if len(r.KeyQuotes) != 2 {
		t.Errorf("KeyQuotes length: got %d, want 2", len(r.KeyQuotes))
	}
	if r.KeyQuotes[0] != "situation is critical" {
		t.Errorf("KeyQuotes[0]: got %q, want %q", r.KeyQuotes[0], "situation is critical")
	}
}

func TestInterviewReport_Fields(t *testing.T) {
	results := []InterviewResult{
		{AgentName: "Alice", Response: "Response A", KeyQuotes: []string{"quote A"}},
		{AgentName: "Bob", Response: "Response B", KeyQuotes: nil},
	}
	report := InterviewReport{
		Question: "What happened?",
		Results:  results,
		Summary:  "Both agents responded thoughtfully.",
	}
	if report.Question != "What happened?" {
		t.Errorf("Question: got %q, want %q", report.Question, "What happened?")
	}
	if len(report.Results) != 2 {
		t.Fatalf("Results length: got %d, want 2", len(report.Results))
	}
	if report.Results[0].AgentName != "Alice" {
		t.Errorf("Results[0].AgentName: got %q, want %q", report.Results[0].AgentName, "Alice")
	}
	if report.Results[1].KeyQuotes != nil {
		t.Errorf("Results[1].KeyQuotes: expected nil, got %v", report.Results[1].KeyQuotes)
	}
	if report.Summary != "Both agents responded thoughtfully." {
		t.Errorf("Summary mismatch")
	}
}

func TestInterviewReport_EmptyResults(t *testing.T) {
	report := InterviewReport{Question: "Who did it?"}
	if report.Question != "Who did it?" {
		t.Errorf("Question mismatch")
	}
	if len(report.Results) != 0 {
		t.Errorf("expected empty Results, got %d", len(report.Results))
	}
	if report.Summary != "" {
		t.Errorf("expected empty Summary, got %q", report.Summary)
	}
}

// ─── SelectAgents tests ───────────────────────────────────────────────────────

func TestSelectAgents_EmptyPool(t *testing.T) {
	client := stubLLM()
	selected, err := SelectAgents(context.Background(), client, "any question", nil, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(selected) != 0 {
		t.Errorf("expected empty slice, got %v", selected)
	}
}

func TestSelectAgents_MaxNLargerThanPool(t *testing.T) {
	client := stubLLM()
	agents := []string{"Alice", "Bob"}
	selected, err := SelectAgents(context.Background(), client, "question", agents, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return all agents since maxN >= len.
	if len(selected) != 2 {
		t.Errorf("expected 2, got %d: %v", len(selected), selected)
	}
}

func TestSelectAgents_FallbackOnLLMFailure(t *testing.T) {
	// stubLLM returns a provider "none" — JSON() will fail, so SelectAgents
	// should fall back to the first maxN agents gracefully.
	client := stubLLM()
	agents := []string{"Alice", "Bob", "Carol", "Dave"}
	selected, err := SelectAgents(context.Background(), client, "question", agents, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fallback must return exactly maxN from the front of the slice.
	if len(selected) != 2 {
		t.Errorf("expected 2 (fallback), got %d: %v", len(selected), selected)
	}
}

// ─── InterviewStructured smoke test ──────────────────────────────────────────

func TestInterviewStructured_NoNodes(t *testing.T) {
	database, projID := reportTestDB(t)
	client := stubLLM()

	report, err := InterviewStructured(context.Background(), database, client, projID, "What happened?", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.Question != "What happened?" {
		t.Errorf("Question: got %q", report.Question)
	}
	if len(report.Results) != 0 {
		t.Errorf("expected 0 results for empty graph, got %d", len(report.Results))
	}
}

func TestInterviewStructured_WithNodes(t *testing.T) {
	database, projID := reportTestDB(t)
	database.UpsertNode(projID, "Alice", "Person", "a researcher", "{}")
	database.UpsertNode(projID, "Bob", "Person", "a journalist", "{}")

	client := stubLLM()

	// LLM calls will fail (no real API), but the function should degrade gracefully.
	report, err := InterviewStructured(context.Background(), database, client, projID, "What is your view?", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.Question != "What is your view?" {
		t.Errorf("Question: got %q", report.Question)
	}
	// Results count equals the number of selected agents (up to 2).
	if len(report.Results) > 2 {
		t.Errorf("expected at most 2 results, got %d", len(report.Results))
	}
}

// ─── Interview (export check) ─────────────────────────────────────────────────

func TestInterview_AgentNotFound(t *testing.T) {
	database, projID := reportTestDB(t)
	agent := New(database, stubLLM())

	_, _, err := agent.Interview(context.Background(), projID, "Ghost", "hello?", nil)
	if err == nil {
		t.Error("expected error for unknown agent, got nil")
	}
}

func TestInterview_FindsByName(t *testing.T) {
	database, projID := reportTestDB(t)
	database.UpsertNode(projID, "Alice", "Person", "a scientist", "{}")

	agent := New(database, stubLLM())
	// This will fail at LLM call (no API), but should not fail on node lookup.
	// We only verify the error is not a "not found" error.
	_, _, err := agent.Interview(context.Background(), projID, "Alice", "hello?", nil)
	// LLM call may fail — that's fine. We just check it isn't a "not found" err.
	if err != nil && err.Error() == `agent "Alice" not found in graph` {
		t.Error("Interview should have found Alice")
	}
}
