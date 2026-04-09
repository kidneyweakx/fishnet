package platform

import (
	"testing"
)

// ─── validateStance ───────────────────────────────────────────────────────────

func TestValidateStance_ValidValues(t *testing.T) {
	valid := []string{"supportive", "opposing", "neutral", "observer"}
	for _, s := range valid {
		got := validateStance(s)
		if got != s {
			t.Errorf("validateStance(%q) = %q, want %q", s, got, s)
		}
	}
}

func TestValidateStance_InvalidFallsBackToNeutral(t *testing.T) {
	invalid := []string{"", "random", "NEUTRAL", "pro", "con"}
	for _, s := range invalid {
		got := validateStance(s)
		if got != "neutral" {
			t.Errorf("validateStance(%q) = %q, want neutral", s, got)
		}
	}
}

// ─── clampSentimentBias ───────────────────────────────────────────────────────

func TestClampSentimentBias(t *testing.T) {
	tests := []struct {
		in, want float64
	}{
		{-2.0, -1.0},
		{-1.0, -1.0},
		{-0.5, -0.5},
		{0.0, 0.0},
		{0.5, 0.5},
		{1.0, 1.0},
		{2.0, 1.0},
	}
	for _, tc := range tests {
		got := clampSentimentBias(tc.in)
		if got != tc.want {
			t.Errorf("clampSentimentBias(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ─── clampInfluenceWeight ─────────────────────────────────────────────────────

func TestClampInfluenceWeight(t *testing.T) {
	tests := []struct {
		in, want float64
	}{
		{-1.0, 0.0},
		{0.0, 0.0},
		{1.0, 1.0},
		{2.0, 2.0},
		{3.0, 2.0},
	}
	for _, tc := range tests {
		got := clampInfluenceWeight(tc.in)
		if got != tc.want {
			t.Errorf("clampInfluenceWeight(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ─── clampUnit ────────────────────────────────────────────────────────────────

func TestClampUnit(t *testing.T) {
	tests := []struct {
		in, want float64
	}{
		{-0.5, 0.0},
		{0.0, 0.0},
		{0.5, 0.5},
		{1.0, 1.0},
		{1.5, 1.0},
	}
	for _, tc := range tests {
		got := clampUnit(tc.in)
		if got != tc.want {
			t.Errorf("clampUnit(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ─── clampPositive ────────────────────────────────────────────────────────────

func TestClampPositive_ReturnsFallbackWhenNonPositive(t *testing.T) {
	got := clampPositive(0.0, 1.5)
	if got != 1.5 {
		t.Errorf("clampPositive(0, 1.5) = %v, want 1.5", got)
	}
	got2 := clampPositive(-1.0, 2.0)
	if got2 != 2.0 {
		t.Errorf("clampPositive(-1, 2.0) = %v, want 2.0", got2)
	}
}

func TestClampPositive_ReturnsValueWhenPositive(t *testing.T) {
	got := clampPositive(3.5, 1.0)
	if got != 3.5 {
		t.Errorf("clampPositive(3.5, 1.0) = %v, want 3.5", got)
	}
}

// ─── agentSimConfigDefaults ───────────────────────────────────────────────────

func TestAgentSimConfigDefaults(t *testing.T) {
	def := agentSimConfigDefaults("node-1")

	if def.AgentID != "node-1" {
		t.Errorf("AgentID = %q, want node-1", def.AgentID)
	}
	if def.Stance != "neutral" {
		t.Errorf("Stance = %q, want neutral", def.Stance)
	}
	if def.SentimentBias != 0.0 {
		t.Errorf("SentimentBias = %v, want 0.0", def.SentimentBias)
	}
	if def.PostsPerHour != 1.0 {
		t.Errorf("PostsPerHour = %v, want 1.0", def.PostsPerHour)
	}
	if def.CommentsPerHour != 2.0 {
		t.Errorf("CommentsPerHour = %v, want 2.0", def.CommentsPerHour)
	}
	if def.InfluenceWeight != 1.0 {
		t.Errorf("InfluenceWeight = %v, want 1.0", def.InfluenceWeight)
	}
	if len(def.ActiveHours) == 0 {
		t.Error("ActiveHours should be non-empty")
	}
}

// ─── defaultActivityByHour ────────────────────────────────────────────────────

func TestDefaultActivityByHour_TaipeiPeakHours(t *testing.T) {
	activity := defaultActivityByHour("Asia/Taipei")

	// Peak evening hours (19-22) should have high activity
	for h := 19; h <= 22; h++ {
		if activity[h] < 1.0 {
			t.Errorf("Asia/Taipei hour %d activity = %v, want >= 1.0", h, activity[h])
		}
	}

	// Dead hours (0-5) should have low activity
	for h := 0; h <= 5; h++ {
		if activity[h] > 0.1 {
			t.Errorf("Asia/Taipei hour %d activity = %v, want <= 0.1", h, activity[h])
		}
	}
}

func TestDefaultActivityByHour_Shanghai(t *testing.T) {
	// Asia/Shanghai should behave the same as Asia/Taipei
	taipei := defaultActivityByHour("Asia/Taipei")
	shanghai := defaultActivityByHour("Asia/Shanghai")
	for h := 0; h < 24; h++ {
		if taipei[h] != shanghai[h] {
			t.Errorf("hour %d: Taipei=%v, Shanghai=%v (should be identical)", h, taipei[h], shanghai[h])
		}
	}
}

func TestDefaultActivityByHour_USTimezones(t *testing.T) {
	usTZs := []string{"America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles"}
	for _, tz := range usTZs {
		activity := defaultActivityByHour(tz)
		// Length should always be 24
		if len(activity) != 24 {
			t.Errorf("%s: activity length = %d, want 24", tz, len(activity))
		}
		// Evening hours should be active
		if activity[19] <= 1.0 {
			t.Errorf("%s: hour 19 activity = %v, want > 1.0", tz, activity[19])
		}
	}
}

func TestDefaultActivityByHour_UnknownFallback(t *testing.T) {
	activity := defaultActivityByHour("Unknown/Zone")

	if len(activity) != 24 {
		t.Errorf("unknown timezone: activity length = %d, want 24", len(activity))
	}
	// Verify all values are non-negative
	for h := 0; h < 24; h++ {
		if activity[h] < 0 {
			t.Errorf("unknown timezone hour %d: activity = %v, want >= 0", h, activity[h])
		}
	}
}

func TestDefaultActivityByHour_AllHoursPresent(t *testing.T) {
	timezones := []string{
		"Asia/Taipei", "Asia/Shanghai", "Asia/Hong_Kong", "Asia/Singapore",
		"America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles",
		"UTC",
	}
	for _, tz := range timezones {
		activity := defaultActivityByHour(tz)
		if len(activity) != 24 {
			t.Errorf("%s: expected 24 hour entries, got %d", tz, len(activity))
		}
	}
}

// ─── truncateScenario ─────────────────────────────────────────────────────────

func TestTruncateScenario_Short(t *testing.T) {
	s := "a brief scenario"
	got := truncateScenario(s)
	if got != s {
		t.Errorf("truncateScenario(%q) = %q, want unchanged", s, got)
	}
}

func TestTruncateScenario_Exactly80(t *testing.T) {
	s := ""
	for i := 0; i < 80; i++ {
		s += "x"
	}
	got := truncateScenario(s)
	if got != s {
		t.Errorf("truncateScenario with 80-char string should be unchanged")
	}
}

func TestTruncateScenario_Over80(t *testing.T) {
	s := ""
	for i := 0; i < 100; i++ {
		s += "a"
	}
	got := truncateScenario(s)
	runes := []rune(got)
	if len(runes) != 81 { // 80 + ellipsis
		t.Errorf("truncateScenario: expected 81 runes (80 + ellipsis), got %d", len(runes))
	}
}

// ─── ApplySimConfig ───────────────────────────────────────────────────────────

func TestApplySimConfig_MatchByAgentID(t *testing.T) {
	personalities := []*Personality{
		{AgentID: "agent-1", Name: "Alice", Stance: "neutral"},
		{AgentID: "agent-2", Name: "Bob", Stance: "neutral"},
	}

	cfg := &SimConfig{
		AgentCfgs: []AgentSimConfig{
			{
				AgentID:         "agent-1",
				Stance:          "supportive",
				SentimentBias:   0.5,
				PostsPerHour:    2.0,
				CommentsPerHour: 3.0,
				InfluenceWeight: 1.5,
				ActiveHours:     []int{9, 10, 11},
			},
		},
	}

	ApplySimConfig(personalities, cfg)

	if personalities[0].Stance != "supportive" {
		t.Errorf("agent-1 Stance = %q, want supportive", personalities[0].Stance)
	}
	if personalities[0].SentimentBias != 0.5 {
		t.Errorf("agent-1 SentimentBias = %v, want 0.5", personalities[0].SentimentBias)
	}
	if personalities[0].PostsPerHour != 2.0 {
		t.Errorf("agent-1 PostsPerHour = %v, want 2.0", personalities[0].PostsPerHour)
	}
	if personalities[0].InfluenceWeight != 1.5 {
		t.Errorf("agent-1 InfluenceWeight = %v, want 1.5", personalities[0].InfluenceWeight)
	}

	// agent-2 should be unchanged
	if personalities[1].Stance != "neutral" {
		t.Errorf("agent-2 Stance = %q, want neutral (unchanged)", personalities[1].Stance)
	}
}

func TestApplySimConfig_RichPersonaFields(t *testing.T) {
	personalities := []*Personality{
		{AgentID: "agent-1", Name: "Alice"},
	}

	cfg := &SimConfig{
		AgentCfgs: []AgentSimConfig{
			{
				AgentID:      "agent-1",
				Stance:       "neutral",
				Username:     "alice123",
				RealName:     "Alice Smith",
				Profession:   "Engineer",
				Creativity:   0.8,
				Rationality:  0.7,
				Empathy:      0.6,
				Extraversion: 0.5,
				Openness:     0.9,
				PostingStyle: "technical",
				Catchphrases: []string{"hello world", "code is life"},
				Topics:       []string{"tech", "science"},
				ActiveHours:  []int{9, 10},
			},
		},
	}

	ApplySimConfig(personalities, cfg)

	p := personalities[0]
	if p.Username != "alice123" {
		t.Errorf("Username = %q, want alice123", p.Username)
	}
	if p.RealName != "Alice Smith" {
		t.Errorf("RealName = %q, want Alice Smith", p.RealName)
	}
	if p.Profession != "Engineer" {
		t.Errorf("Profession = %q, want Engineer", p.Profession)
	}
	if p.Creativity != 0.8 {
		t.Errorf("Creativity = %v, want 0.8", p.Creativity)
	}
	if p.PostStyle != "technical" {
		t.Errorf("PostStyle = %q, want technical", p.PostStyle)
	}
	if len(p.Catchphrases) != 2 {
		t.Errorf("Catchphrases = %v, want 2 items", p.Catchphrases)
	}
	if len(p.Interests) != 2 {
		t.Errorf("Interests (Topics) = %v, want 2 items", p.Interests)
	}
}

func TestApplySimConfig_NilPersonalitiesSkipped(t *testing.T) {
	personalities := []*Personality{nil, {AgentID: "agent-2", Name: "Bob"}}

	cfg := &SimConfig{
		AgentCfgs: []AgentSimConfig{
			{AgentID: "agent-2", Stance: "opposing", ActiveHours: []int{9}},
		},
	}

	// Should not panic on nil personality
	ApplySimConfig(personalities, cfg)

	if personalities[1].Stance != "opposing" {
		t.Errorf("agent-2 Stance = %q, want opposing", personalities[1].Stance)
	}
}

func TestApplySimConfig_NoMatchIsNoOp(t *testing.T) {
	personalities := []*Personality{
		{AgentID: "agent-1", Name: "Alice", Stance: "neutral", PostsPerHour: 1.0},
	}

	cfg := &SimConfig{
		AgentCfgs: []AgentSimConfig{
			{AgentID: "agent-99", Stance: "opposing", PostsPerHour: 5.0},
		},
	}

	ApplySimConfig(personalities, cfg)

	if personalities[0].Stance != "neutral" {
		t.Errorf("unmatched agent should keep Stance neutral, got %q", personalities[0].Stance)
	}
	if personalities[0].PostsPerHour != 1.0 {
		t.Errorf("unmatched agent should keep PostsPerHour 1.0, got %v", personalities[0].PostsPerHour)
	}
}

// ─── AgentSimConfig struct ────────────────────────────────────────────────────

func TestAgentSimConfig_ZeroValue(t *testing.T) {
	cfg := AgentSimConfig{}
	if cfg.AgentID != "" {
		t.Errorf("zero-value AgentID = %q, want empty", cfg.AgentID)
	}
	if cfg.InfluenceWeight != 0 {
		t.Errorf("zero-value InfluenceWeight = %v, want 0", cfg.InfluenceWeight)
	}
}

func TestAgentSimConfig_Fields(t *testing.T) {
	cfg := AgentSimConfig{
		AgentID:         "test-id",
		Stance:          "observer",
		SentimentBias:   -0.3,
		PostsPerHour:    1.5,
		CommentsPerHour: 4.0,
		ActiveHours:     []int{8, 9, 10, 11},
		InfluenceWeight: 0.8,
		Username:        "testuser",
		RealName:        "Test User",
		Profession:      "Researcher",
		Creativity:      0.7,
		PostingStyle:    "analytical",
		Catchphrases:    []string{"data is king"},
		Topics:          []string{"research"},
	}

	if cfg.AgentID != "test-id" {
		t.Errorf("AgentID = %q", cfg.AgentID)
	}
	if cfg.Stance != "observer" {
		t.Errorf("Stance = %q", cfg.Stance)
	}
	if len(cfg.ActiveHours) != 4 {
		t.Errorf("ActiveHours length = %d, want 4", len(cfg.ActiveHours))
	}
}
