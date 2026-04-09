package sim

import (
	"context"
	"testing"
	"time"

	"fishnet/internal/platform"
	"fishnet/internal/session"
)

// ─── InterventionQueue ────────────────────────────────────────────────────────

func TestInterventionQueue_Basic(t *testing.T) {
	q := NewInterventionQueue()

	// Push events for different rounds
	q.Add(InterventionEvent{Round: 1, Type: "inject_post", Content: "hello round 1"})
	q.Add(InterventionEvent{Round: 2, Type: "trending_topic", Content: "AI"})
	q.Add(InterventionEvent{Round: 1, Type: "inject_post", Content: "second round 1 post"})

	// Drain round 1 — should get 2 events
	events := q.Drain(1)
	if len(events) != 2 {
		t.Fatalf("Drain(1) = %d events, want 2", len(events))
	}
	for _, e := range events {
		if e.Round != 1 {
			t.Errorf("unexpected round in drained event: %d", e.Round)
		}
	}

	// Drain round 1 again — queue should be empty for round 1
	events2 := q.Drain(1)
	if len(events2) != 0 {
		t.Errorf("second Drain(1) = %d events, want 0", len(events2))
	}

	// Drain round 2
	events3 := q.Drain(2)
	if len(events3) != 1 {
		t.Fatalf("Drain(2) = %d events, want 1", len(events3))
	}
	if events3[0].Type != "trending_topic" {
		t.Errorf("event type = %q, want %q", events3[0].Type, "trending_topic")
	}
}

func TestInterventionQueue_ZeroRoundTreatedAsOne(t *testing.T) {
	q := NewInterventionQueue()
	q.Add(InterventionEvent{Round: 0, Type: "inject_post", Content: "ASAP"})

	// Round 0 should match when Drain(1) is called
	events := q.Drain(1)
	if len(events) != 1 {
		t.Fatalf("Drain(1) for Round=0 event = %d, want 1", len(events))
	}
}

func TestInterventionQueue_DrainNonMatchingRound(t *testing.T) {
	q := NewInterventionQueue()
	q.Add(InterventionEvent{Round: 5, Type: "inject_post", Content: "future"})

	events := q.Drain(1)
	if len(events) != 0 {
		t.Errorf("Drain(1) for round 5 event = %d, want 0", len(events))
	}

	// Event should still be present for round 5
	events5 := q.Drain(5)
	if len(events5) != 1 {
		t.Errorf("Drain(5) = %d, want 1", len(events5))
	}
}

func TestInterventionQueue_PauseResume(t *testing.T) {
	q := NewInterventionQueue()

	// drainResume on empty queue returns false
	if q.drainResume() {
		t.Error("drainResume on empty queue should return false")
	}

	// Add a non-resume event — drainResume should still return false
	q.Add(InterventionEvent{Type: "inject_post", Round: 1})
	if q.drainResume() {
		t.Error("drainResume without resume event should return false")
	}

	// Add a resume event — drainResume should return true and remove it
	q.Add(InterventionEvent{Type: "resume", Round: 1})
	if !q.drainResume() {
		t.Error("drainResume with resume event should return true")
	}

	// After consuming, only inject_post should remain
	remaining := q.Drain(1)
	if len(remaining) != 1 {
		t.Fatalf("after drainResume, remaining events = %d, want 1", len(remaining))
	}
	if remaining[0].Type != "inject_post" {
		t.Errorf("remaining event type = %q, want inject_post", remaining[0].Type)
	}
}

func TestInterventionQueue_Drain_drainResume(t *testing.T) {
	q := NewInterventionQueue()

	// Add multiple events including resume in the middle
	q.Add(InterventionEvent{Type: "inject_post", Round: 1})
	q.Add(InterventionEvent{Type: "resume"})
	q.Add(InterventionEvent{Type: "trending_topic", Round: 2})

	// drainResume should remove only the first "resume"
	got := q.drainResume()
	if !got {
		t.Fatal("drainResume should find and return true")
	}

	// Two events remain
	all1 := q.Drain(1)
	if len(all1) != 1 || all1[0].Type != "inject_post" {
		t.Errorf("unexpected round-1 events after drainResume: %v", all1)
	}
	all2 := q.Drain(2)
	if len(all2) != 1 || all2[0].Type != "trending_topic" {
		t.Errorf("unexpected round-2 events after drainResume: %v", all2)
	}
}

func TestInterventionQueue_ConcurrentAdd(t *testing.T) {
	q := NewInterventionQueue()
	done := make(chan struct{})

	go func() {
		for i := 0; i < 50; i++ {
			q.Add(InterventionEvent{Round: 1, Type: "inject_post"})
		}
		close(done)
	}()

	for i := 0; i < 50; i++ {
		q.Add(InterventionEvent{Round: 1, Type: "inject_post"})
	}

	<-done
	events := q.Drain(1)
	if len(events) != 100 {
		t.Errorf("concurrent Add: got %d events, want 100", len(events))
	}
}

// ─── ApplyIntervention ────────────────────────────────────────────────────────

func TestApplyIntervention_InjectPost(t *testing.T) {
	state := platform.NewState("twitter")
	event := InterventionEvent{
		Type:    "inject_post",
		Content: "Test injection #hello",
	}

	statsBefore := state.GetStats()
	ApplyIntervention(state, event, "proj-1")
	statsAfter := state.GetStats()

	if statsAfter.Posts != statsBefore.Posts+1 {
		t.Errorf("inject_post: posts went from %d to %d, expected +1",
			statsBefore.Posts, statsAfter.Posts)
	}
}

func TestApplyIntervention_TrendingTopic(t *testing.T) {
	state := platform.NewState("twitter")
	event := InterventionEvent{
		Type:    "trending_topic",
		Content: "ArtificialIntelligence",
	}

	statsBefore := state.GetStats()
	ApplyIntervention(state, event, "proj-2")
	statsAfter := state.GetStats()

	// trending_topic injects 3 posts from synthetic agents
	if statsAfter.Posts != statsBefore.Posts+3 {
		t.Errorf("trending_topic: posts went from %d to %d, expected +3",
			statsBefore.Posts, statsAfter.Posts)
	}
}

func TestApplyIntervention_PauseResumeNoOp(t *testing.T) {
	state := platform.NewState("twitter")

	statsBefore := state.GetStats()

	// pause and resume should not mutate state
	ApplyIntervention(state, InterventionEvent{Type: "pause"}, "proj-3")
	ApplyIntervention(state, InterventionEvent{Type: "resume"}, "proj-3")
	ApplyIntervention(state, InterventionEvent{Type: "agent_directive", AgentID: "a1", Message: "be nice"}, "proj-3")

	statsAfter := state.GetStats()
	if statsAfter.Posts != statsBefore.Posts {
		t.Errorf("pause/resume/agent_directive should not add posts: before=%d after=%d",
			statsBefore.Posts, statsAfter.Posts)
	}
}

// ─── waitForResume ────────────────────────────────────────────────────────────

func TestWaitForResume_ContextCancel(t *testing.T) {
	q := NewInterventionQueue()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := waitForResume(ctx, q)
	if err == nil {
		t.Error("waitForResume should return error when context is cancelled")
	}
}

func TestWaitForResume_ResumeEvent(t *testing.T) {
	q := NewInterventionQueue()
	ctx := context.Background()

	// Schedule a resume to be added after a short delay
	go func() {
		time.Sleep(300 * time.Millisecond)
		q.Add(InterventionEvent{Type: "resume"})
	}()

	err := waitForResume(ctx, q)
	if err != nil {
		t.Errorf("waitForResume should return nil when resume arrives, got: %v", err)
	}
}

// ─── SetProgress ──────────────────────────────────────────────────────────────

func TestSetProgress_Basic(t *testing.T) {
	s := &session.Session{}

	s.SetProgress(5, 10)
	if s.Progress != 50 {
		t.Errorf("SetProgress(5,10) = %d, want 50", s.Progress)
	}
}

func TestSetProgress_Zero(t *testing.T) {
	s := &session.Session{}

	s.SetProgress(0, 10)
	if s.Progress != 0 {
		t.Errorf("SetProgress(0,10) = %d, want 0", s.Progress)
	}
}

func TestSetProgress_Full(t *testing.T) {
	s := &session.Session{}

	s.SetProgress(10, 10)
	if s.Progress != 100 {
		t.Errorf("SetProgress(10,10) = %d, want 100", s.Progress)
	}
}

func TestSetProgress_OverFull(t *testing.T) {
	s := &session.Session{}

	// More than maxRounds (shouldn't happen, but guard against it)
	s.SetProgress(15, 10)
	if s.Progress != 100 {
		t.Errorf("SetProgress(15,10) = %d, want 100 (capped)", s.Progress)
	}
}

func TestSetProgress_ZeroMaxRounds(t *testing.T) {
	s := &session.Session{Progress: 42}

	// Should be a no-op when maxRounds <= 0
	s.SetProgress(5, 0)
	if s.Progress != 42 {
		t.Errorf("SetProgress with maxRounds=0 changed progress to %d, want 42", s.Progress)
	}
}

func TestSetProgress_Sequence(t *testing.T) {
	s := &session.Session{}

	tests := []struct {
		round, max int
		wantPct    int
	}{
		{1, 10, 10},
		{2, 10, 20},
		{5, 10, 50},
		{9, 10, 90},
		{10, 10, 100},
	}

	for _, tc := range tests {
		s.SetProgress(tc.round, tc.max)
		if s.Progress != tc.wantPct {
			t.Errorf("SetProgress(%d,%d) = %d, want %d", tc.round, tc.max, s.Progress, tc.wantPct)
		}
	}
}
