package sim

import (
	"testing"

	"fishnet/internal/platform"
)

// ─── RoundProgress ────────────────────────────────────────────────────────────

func TestRoundProgress_Defaults(t *testing.T) {
	rp := RoundProgress{}
	if rp.Done {
		t.Error("zero-value RoundProgress.Done should be false")
	}
	if rp.Paused {
		t.Error("zero-value RoundProgress.Paused should be false")
	}
	if rp.Error != nil {
		t.Errorf("zero-value RoundProgress.Error should be nil, got %v", rp.Error)
	}
	if rp.Intervention != nil {
		t.Errorf("zero-value RoundProgress.Intervention should be nil")
	}
}

func TestRoundProgress_Fields(t *testing.T) {
	action := platform.Action{
		Round:    3,
		Platform: "twitter",
		AgentID:  "agent-1",
		Type:     "CREATE_POST",
		Success:  true,
	}
	iv := &InterventionEvent{Type: "inject_post", Round: 3}

	rp := RoundProgress{
		Round:        3,
		MaxRounds:    10,
		Action:       action,
		TwitterStat:  platform.Stats{Posts: 5, Likes: 12},
		RedditStat:   platform.Stats{Posts: 2},
		Done:         false,
		Paused:       false,
		Intervention: iv,
	}

	if rp.Round != 3 {
		t.Errorf("Round = %d, want 3", rp.Round)
	}
	if rp.MaxRounds != 10 {
		t.Errorf("MaxRounds = %d, want 10", rp.MaxRounds)
	}
	if rp.Action.Platform != "twitter" {
		t.Errorf("Action.Platform = %q, want twitter", rp.Action.Platform)
	}
	if rp.TwitterStat.Posts != 5 {
		t.Errorf("TwitterStat.Posts = %d, want 5", rp.TwitterStat.Posts)
	}
	if rp.TwitterStat.Likes != 12 {
		t.Errorf("TwitterStat.Likes = %d, want 12", rp.TwitterStat.Likes)
	}
	if rp.RedditStat.Posts != 2 {
		t.Errorf("RedditStat.Posts = %d, want 2", rp.RedditStat.Posts)
	}
	if rp.Intervention == nil {
		t.Fatal("Intervention should not be nil")
	}
	if rp.Intervention.Type != "inject_post" {
		t.Errorf("Intervention.Type = %q, want inject_post", rp.Intervention.Type)
	}
}

func TestRoundProgress_DoneFlag(t *testing.T) {
	rp := RoundProgress{
		Round:     10,
		MaxRounds: 10,
		Done:      true,
	}
	if !rp.Done {
		t.Error("Done should be true")
	}
}

func TestRoundProgress_PausedFlag(t *testing.T) {
	iv := &InterventionEvent{Type: "pause", Round: 2}
	rp := RoundProgress{
		Round:        2,
		MaxRounds:    10,
		Paused:       true,
		Intervention: iv,
	}
	if !rp.Paused {
		t.Error("Paused should be true")
	}
	if rp.Intervention.Type != "pause" {
		t.Errorf("Intervention.Type = %q, want pause", rp.Intervention.Type)
	}
}

// ─── RoundConfig ──────────────────────────────────────────────────────────────

func TestRoundConfig_Defaults(t *testing.T) {
	cfg := RoundConfig{}
	if cfg.MaxRounds != 0 {
		t.Errorf("default MaxRounds = %d, want 0", cfg.MaxRounds)
	}
	if cfg.Concurrency != 0 {
		t.Errorf("default Concurrency = %d, want 0", cfg.Concurrency)
	}
	if cfg.InterventionQueue != nil {
		t.Error("default InterventionQueue should be nil")
	}
}

func TestRoundConfig_WithQueue(t *testing.T) {
	q := NewInterventionQueue()
	cfg := RoundConfig{
		Scenario:          "test scenario",
		MaxRounds:         5,
		MaxAgents:         10,
		Platforms:         []string{"twitter"},
		Concurrency:       4,
		InterventionQueue: q,
	}

	if cfg.Scenario != "test scenario" {
		t.Errorf("Scenario = %q, want test scenario", cfg.Scenario)
	}
	if cfg.MaxRounds != 5 {
		t.Errorf("MaxRounds = %d, want 5", cfg.MaxRounds)
	}
	if cfg.InterventionQueue == nil {
		t.Error("InterventionQueue should not be nil")
	}
}

// ─── hasPlatform helper ───────────────────────────────────────────────────────

func TestHasPlatform_EmptyList(t *testing.T) {
	// Empty platforms means "all" platforms
	if !hasPlatform(nil, "twitter") {
		t.Error("empty platform list should match any platform")
	}
	if !hasPlatform([]string{}, "reddit") {
		t.Error("empty platform list should match any platform")
	}
}

func TestHasPlatform_Match(t *testing.T) {
	platforms := []string{"twitter", "reddit"}
	if !hasPlatform(platforms, "twitter") {
		t.Error("should match twitter")
	}
	if !hasPlatform(platforms, "reddit") {
		t.Error("should match reddit")
	}
}

func TestHasPlatform_NoMatch(t *testing.T) {
	platforms := []string{"twitter"}
	if hasPlatform(platforms, "reddit") {
		t.Error("should not match reddit when only twitter is listed")
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func TestClamp01(t *testing.T) {
	tests := []struct {
		in, want float64
	}{
		{-1.0, 0.0},
		{0.0, 0.0},
		{0.5, 0.5},
		{1.0, 1.0},
		{1.5, 1.0},
	}
	for _, tc := range tests {
		got := clamp01(tc.in)
		if got != tc.want {
			t.Errorf("clamp01(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestTruncScenario_Short(t *testing.T) {
	s := "short scenario"
	got := truncScenario(s)
	if got != s {
		t.Errorf("truncScenario(%q) = %q, want unchanged", s, got)
	}
}

func TestTruncScenario_Long(t *testing.T) {
	// Build a string of 2500 runes (above the 2000-rune truncation limit)
	long := ""
	for i := 0; i < 2500; i++ {
		long += "a"
	}
	got := truncScenario(long)
	// Should be truncated to 2000 runes + "…"
	runes := []rune(got)
	if len(runes) != 2001 { // 2000 chars + 1 ellipsis rune
		t.Errorf("truncScenario: expected 2001 runes, got %d", len(runes))
	}
}
