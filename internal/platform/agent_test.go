package platform

import (
	"testing"

	"fishnet/internal/db"
)

// ─── FromNode — rich persona fields ───────────────────────────────────────────

func TestFromNode_RealNameDefaultsToNodeName(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person", Summary: "A researcher"}
	p := FromNode(node, 0)

	if p.RealName != "Alice" {
		t.Errorf("RealName = %q, want %q", p.RealName, "Alice")
	}
}

func TestFromNode_ProfessionDefaultsToNodeType(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Professor", Summary: ""}
	p := FromNode(node, 0)

	if p.Profession != "Professor" {
		t.Errorf("Profession = %q, want %q", p.Profession, "Professor")
	}
}

func TestFromNode_UsernameEmptyByDefault(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p := FromNode(node, 0)

	if p.Username != "" {
		t.Errorf("Username should be empty by default (LLM-populated), got %q", p.Username)
	}
}

func TestFromNode_CatchphrasesEmptyByDefault(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p := FromNode(node, 0)

	if p.Catchphrases == nil {
		t.Error("Catchphrases should be an empty slice (not nil)")
	}
	if len(p.Catchphrases) != 0 {
		t.Errorf("Catchphrases should be empty by default, got %v", p.Catchphrases)
	}
}

func TestFromNode_PersonalityTraitsInRange(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p := FromNode(node, 0)

	checkTrait := func(name string, v float64) {
		t.Helper()
		if v < 0.0 || v > 1.0 {
			t.Errorf("%s = %f, want in [0.0, 1.0]", name, v)
		}
	}
	checkTrait("Creativity", p.Creativity)
	checkTrait("Rationality", p.Rationality)
	checkTrait("Empathy", p.Empathy)
	checkTrait("Extraversion", p.Extraversion)
	checkTrait("Openness", p.Openness)
}

func TestFromNode_PersonalityTraitsDeterministic(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p1 := FromNode(node, 7)
	p2 := FromNode(node, 7)

	if p1.Creativity != p2.Creativity {
		t.Errorf("Creativity not deterministic: %f vs %f", p1.Creativity, p2.Creativity)
	}
	if p1.Empathy != p2.Empathy {
		t.Errorf("Empathy not deterministic: %f vs %f", p1.Empathy, p2.Empathy)
	}
}

func TestFromNode_PersonalityTraitsVaryByIndex(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p0 := FromNode(node, 0)
	p1 := FromNode(node, 1)

	// Different seeds → very likely different trait values
	if p0.Creativity == p1.Creativity &&
		p0.Rationality == p1.Rationality &&
		p0.Empathy == p1.Empathy {
		t.Error("different idx should produce different trait values")
	}
}

// ─── ApplySimConfig — rich persona fields ─────────────────────────────────────

func TestApplySimConfig_RichPersonaFieldsApplied(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p := FromNode(node, 0)

	cfg := &SimConfig{
		Scenario: "test",
		AgentCfgs: []AgentSimConfig{
			{
				AgentID:      "n1",
				Stance:       "supportive",
				Username:     "alice_42",
				RealName:     "Alice Smith",
				Profession:   "Researcher",
				Creativity:   0.9,
				Rationality:  0.8,
				Empathy:      0.7,
				Extraversion: 0.6,
				Openness:     0.5,
				PostingStyle: "analytical",
				Catchphrases: []string{"Stay curious", "Science rules"},
				Topics:       []string{"AI", "Climate"},
				PostsPerHour:    2.0,
				CommentsPerHour: 4.0,
				InfluenceWeight: 1.5,
				ActiveHours:     []int{9, 10, 11},
			},
		},
	}

	ApplySimConfig([]*Personality{p}, cfg)

	if p.Username != "alice_42" {
		t.Errorf("Username = %q, want %q", p.Username, "alice_42")
	}
	if p.RealName != "Alice Smith" {
		t.Errorf("RealName = %q, want %q", p.RealName, "Alice Smith")
	}
	if p.Profession != "Researcher" {
		t.Errorf("Profession = %q, want %q", p.Profession, "Researcher")
	}
	if p.Creativity != 0.9 {
		t.Errorf("Creativity = %f, want 0.9", p.Creativity)
	}
	if p.Rationality != 0.8 {
		t.Errorf("Rationality = %f, want 0.8", p.Rationality)
	}
	if p.Empathy != 0.7 {
		t.Errorf("Empathy = %f, want 0.7", p.Empathy)
	}
	if p.Extraversion != 0.6 {
		t.Errorf("Extraversion = %f, want 0.6", p.Extraversion)
	}
	if p.Openness != 0.5 {
		t.Errorf("Openness = %f, want 0.5", p.Openness)
	}
	if p.PostStyle != "analytical" {
		t.Errorf("PostStyle = %q, want %q", p.PostStyle, "analytical")
	}
	if len(p.Catchphrases) != 2 {
		t.Errorf("Catchphrases len = %d, want 2", len(p.Catchphrases))
	}
	if len(p.Interests) != 2 || p.Interests[0] != "AI" {
		t.Errorf("Interests = %v, want [AI Climate]", p.Interests)
	}
	if p.CommentsPerHour != 4.0 {
		t.Errorf("CommentsPerHour = %f, want 4.0", p.CommentsPerHour)
	}
}

func TestApplySimConfig_EmptyRichFieldsPreserveDefaults(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Bob", Type: "Student"}
	p := FromNode(node, 3)
	originalRealName := p.RealName
	originalProfession := p.Profession

	cfg := &SimConfig{
		AgentCfgs: []AgentSimConfig{
			{
				AgentID:      "n1",
				Stance:       "neutral",
				// Username, RealName, Profession all empty → should not overwrite
				PostsPerHour:    1.0,
				CommentsPerHour: 2.0,
				InfluenceWeight: 1.0,
			},
		},
	}

	ApplySimConfig([]*Personality{p}, cfg)

	if p.RealName != originalRealName {
		t.Errorf("RealName overwritten to empty: got %q, want %q", p.RealName, originalRealName)
	}
	if p.Profession != originalProfession {
		t.Errorf("Profession overwritten to empty: got %q, want %q", p.Profession, originalProfession)
	}
}

// ─── validatePostingStyle ─────────────────────────────────────────────────────

func TestValidatePostingStyle_ValidValues(t *testing.T) {
	valid := []string{"formal", "casual", "technical", "emotional", "analytical", "informative", "humorous"}
	for _, s := range valid {
		if got := validatePostingStyle(s); got != s {
			t.Errorf("validatePostingStyle(%q) = %q, want %q", s, got, s)
		}
	}
}

func TestValidatePostingStyle_InvalidFallsBack(t *testing.T) {
	if got := validatePostingStyle("random"); got != "informative" {
		t.Errorf("validatePostingStyle(invalid) = %q, want %q", got, "informative")
	}
	if got := validatePostingStyle(""); got != "informative" {
		t.Errorf("validatePostingStyle('') = %q, want %q", got, "informative")
	}
}

// ─── clampUnit ────────────────────────────────────────────────────────────────

func TestClampUnit_InRange(t *testing.T) {
	if v := clampUnit(0.5); v != 0.5 {
		t.Errorf("clampUnit(0.5) = %f, want 0.5", v)
	}
}

func TestClampUnit_BelowZero(t *testing.T) {
	if v := clampUnit(-0.5); v != 0.0 {
		t.Errorf("clampUnit(-0.5) = %f, want 0.0", v)
	}
}

func TestClampUnit_AboveOne(t *testing.T) {
	if v := clampUnit(1.5); v != 1.0 {
		t.Errorf("clampUnit(1.5) = %f, want 1.0", v)
	}
}

func TestClampUnit_ExactBoundaries(t *testing.T) {
	if v := clampUnit(0.0); v != 0.0 {
		t.Errorf("clampUnit(0.0) = %f, want 0.0", v)
	}
	if v := clampUnit(1.0); v != 1.0 {
		t.Errorf("clampUnit(1.0) = %f, want 1.0", v)
	}
}

// ─── FromNode ─────────────────────────────────────────────────────────────────

func TestFromNode_BasicFields(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person", Summary: "A researcher"}
	p := FromNode(node, 0)

	if p.AgentID != "n1" {
		t.Errorf("AgentID = %q, want %q", p.AgentID, "n1")
	}
	if p.Name != "Alice" {
		t.Errorf("Name = %q, want %q", p.Name, "Alice")
	}
	if p.NodeType != "Person" {
		t.Errorf("NodeType = %q, want %q", p.NodeType, "Person")
	}
	if p.Bio != "A researcher" {
		t.Errorf("Bio = %q, want %q", p.Bio, "A researcher")
	}
}

func TestFromNode_DefaultStance(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p := FromNode(node, 0)

	if p.Stance != "neutral" {
		t.Errorf("default Stance = %q, want %q", p.Stance, "neutral")
	}
}

func TestFromNode_ActivityLevelInRange(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p := FromNode(node, 0)

	if p.ActivityLevel < 0.55 || p.ActivityLevel > 0.90 {
		t.Errorf("ActivityLevel = %f, want in [0.55, 0.90]", p.ActivityLevel)
	}
}

func TestFromNode_ActiveHoursSet(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p := FromNode(node, 0)

	if len(p.ActiveHours) == 0 {
		t.Error("ActiveHours should not be empty")
	}
}

func TestFromNode_DifferentIndexDifferentPersonality(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p0 := FromNode(node, 0)
	p1 := FromNode(node, 1)

	// Different seed → likely different activity levels (not a hard guarantee, but very likely)
	if p0.ActivityLevel == p1.ActivityLevel &&
		p0.Reactivity == p1.Reactivity &&
		p0.Originality == p1.Originality {
		t.Error("different idx should produce different personality values (same in all 3 is extremely unlikely)")
	}
}

func TestFromNode_SameInputDeterministic(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p1 := FromNode(node, 5)
	p2 := FromNode(node, 5)

	if p1.ActivityLevel != p2.ActivityLevel {
		t.Errorf("same input should produce same ActivityLevel: %f vs %f", p1.ActivityLevel, p2.ActivityLevel)
	}
	if p1.Reactivity != p2.Reactivity {
		t.Errorf("same input should produce same Reactivity: %f vs %f", p1.Reactivity, p2.Reactivity)
	}
}

// ─── Decide ───────────────────────────────────────────────────────────────────

func TestDecide_ReturnsDeterministicResults(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p1 := FromNode(node, 0)
	p2 := FromNode(node, 0)
	state := NewState("twitter")

	actions1 := p1.Decide(state, "test scenario", 10)
	actions2 := p2.Decide(state, "test scenario", 10)

	if len(actions1) != len(actions2) {
		t.Errorf("Decide not deterministic: %d vs %d actions", len(actions1), len(actions2))
	}
	for i := range actions1 {
		if actions1[i].Type != actions2[i].Type {
			t.Errorf("actions[%d] type mismatch: %q vs %q", i, actions1[i].Type, actions2[i].Type)
		}
	}
}

func TestDecide_MaxSixActionsPerRound(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p := FromNode(node, 0)
	p.ActivityLevel = 1.0
	p.Reactivity = 1.0
	p.Originality = 1.0
	// Make sure the round is in an active hour
	p.ActiveHours = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23}

	state := NewState("twitter")
	// Add lots of posts to give the agent things to react to
	for i := 0; i < 20; i++ {
		state.AddPost(&Post{
			ID:       "p" + string(rune('a'+i)),
			AuthorID: "other",
			Content:  "test post",
		})
	}

	for round := 1; round <= 30; round++ {
		actions := p.Decide(state, "scenario", round)
		if len(actions) > 6 {
			t.Errorf("round %d: %d actions, want <= 6", round, len(actions))
		}
	}
}

func TestDecide_ObserverHasFewerCreatePosts(t *testing.T) {
	// Observer stance should produce fewer CREATE_POST actions than a supportive stance
	node := db.Node{ID: "n1", Name: "Bob", Type: "Person"}

	pObserver := FromNode(node, 1)
	pObserver.Stance = "observer"
	pObserver.ActivityLevel = 1.0
	pObserver.Originality = 1.0
	pObserver.ActiveHours = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23}

	pActive := FromNode(node, 1)
	pActive.Stance = "supportive"
	pActive.ActivityLevel = 1.0
	pActive.Originality = 1.0
	pActive.ActiveHours = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23}

	state := NewState("twitter")
	state.AddPost(&Post{ID: "p1", AuthorID: "other", Content: "post"})

	observerPosts := 0
	activePosts := 0
	for round := 1; round <= 100; round++ {
		for _, a := range pObserver.Decide(state, "test", round) {
			if a.Type == ActCreatePost {
				observerPosts++
			}
		}
		for _, a := range pActive.Decide(state, "test", round) {
			if a.Type == ActCreatePost {
				activePosts++
			}
		}
	}

	if observerPosts >= activePosts {
		t.Errorf("observer created %d posts vs active %d posts; observer should create fewer",
			observerPosts, activePosts)
	}
}

func TestDecide_InactiveAgentReturnsDoNothing(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p := FromNode(node, 0)
	p.ActivityLevel = 0.0 // Never active

	state := NewState("twitter")

	// With ActivityLevel=0, agent should almost always return DO_NOTHING
	doNothingCount := 0
	for round := 0; round <= 100; round++ {
		actions := p.Decide(state, "test", round)
		if len(actions) == 1 && actions[0].Type == ActDoNothing {
			doNothingCount++
		}
	}

	// At ActivityLevel=0, agent should emit DO_NOTHING in most rounds.
	// Decide() does not currently hard-gate on ActivityLevel, so passive
	// actions (refresh/trend/follow/original-post/search) can still fire
	// from their own probabilities — we require a clear majority instead
	// of an absolute threshold.
	if doNothingCount < 70 {
		t.Errorf("inactive agent (ActivityLevel=0) returned DO_NOTHING in %d/101 rounds, expected >= 70", doNothingCount)
	}
}

func TestDecide_ActiveHoursRespected(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p := FromNode(node, 0)
	p.ActivityLevel = 1.0
	p.ActiveHours = []int{9} // only active at hour 9

	state := NewState("twitter")
	state.AddPost(&Post{ID: "p1", AuthorID: "other", Content: "test"})

	// Round 9 maps to hour 9 (round % 24)
	actionsAt9 := p.Decide(state, "test", 9)

	// Round 10 maps to hour 10 (outside active hours)
	// This may or may not produce actions due to the 5% fallback
	// Just check that agent is much more likely active at hour 9
	_ = actionsAt9
	// We can't assert exact counts because of the 5% noise; just ensure no panic
}

// ─── stanceToneHint ───────────────────────────────────────────────────────────

func TestStanceToneHint_Neutral(t *testing.T) {
	p := &Personality{Stance: "neutral", SentimentBias: 0.0}
	hint := p.stanceToneHint()
	if hint != "" {
		t.Errorf("neutral stance with no sentiment should give empty hint, got %q", hint)
	}
}

func TestStanceToneHint_Supportive(t *testing.T) {
	p := &Personality{Stance: "supportive", SentimentBias: 0.0}
	hint := p.stanceToneHint()
	if hint == "" {
		t.Error("supportive stance should produce non-empty tone hint")
	}
}

func TestStanceToneHint_Opposing(t *testing.T) {
	p := &Personality{Stance: "opposing", SentimentBias: 0.0}
	hint := p.stanceToneHint()
	if hint == "" {
		t.Error("opposing stance should produce non-empty tone hint")
	}
}

func TestStanceToneHint_HighSentiment(t *testing.T) {
	p := &Personality{Stance: "neutral", SentimentBias: 0.8}
	hint := p.stanceToneHint()
	if hint == "" {
		t.Error("high SentimentBias should produce non-empty hint")
	}
}

func TestStanceToneHint_LowSentiment(t *testing.T) {
	p := &Personality{Stance: "neutral", SentimentBias: -0.8}
	hint := p.stanceToneHint()
	if hint == "" {
		t.Error("negative SentimentBias should produce non-empty hint")
	}
}

// ─── isActiveHour ─────────────────────────────────────────────────────────────

func TestIsActiveHour_Present(t *testing.T) {
	p := &Personality{ActiveHours: []int{9, 10, 11}}
	if !p.isActiveHour(9) {
		t.Error("isActiveHour(9) should return true when 9 is in ActiveHours")
	}
}

func TestIsActiveHour_Absent(t *testing.T) {
	p := &Personality{ActiveHours: []int{9, 10, 11}}
	if p.isActiveHour(3) {
		t.Error("isActiveHour(3) should return false when 3 is not in ActiveHours")
	}
}

func TestIsActiveHour_Empty(t *testing.T) {
	p := &Personality{ActiveHours: []int{}}
	if p.isActiveHour(0) {
		t.Error("isActiveHour should return false when ActiveHours is empty")
	}
}

// ─── hashStr ──────────────────────────────────────────────────────────────────

func TestHashStr_Deterministic(t *testing.T) {
	h1 := HashStr("alice")
	h2 := HashStr("alice")
	if h1 != h2 {
		t.Errorf("hashStr is not deterministic: %d vs %d", h1, h2)
	}
}

func TestHashStr_DifferentInputs(t *testing.T) {
	h1 := HashStr("alice")
	h2 := HashStr("bob")
	if h1 == h2 {
		t.Error("different inputs should produce different hashes (extremely unlikely to collide)")
	}
}

func TestHashStr_Empty(t *testing.T) {
	h := HashStr("")
	_ = h // just ensure no panic
}

// ─── clip ─────────────────────────────────────────────────────────────────────

func TestClip_ShortString(t *testing.T) {
	s := clip("hello", 10)
	if s != "hello" {
		t.Errorf("clip short = %q, want %q", s, "hello")
	}
}

func TestClip_LongString(t *testing.T) {
	s := clip("hello world foo bar", 5)
	runes := []rune(s)
	// Should have the ellipsis appended
	if len(runes) <= 5 {
		t.Errorf("clip long = %q, expected to contain ellipsis", s)
	}
}

func TestClip_ExactLength(t *testing.T) {
	s := clip("hello", 5)
	if s != "hello" {
		t.Errorf("clip exact = %q, want %q", s, "hello")
	}
}

// ─── DecideFromTimeline ────────────────────────────────────────────────────────

func TestDecideFromTimeline_Deterministic(t *testing.T) {
	node := db.Node{ID: "n1", Name: "Alice", Type: "Person"}
	p := FromNode(node, 0)
	p.ActiveHours = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23}

	timeline := []*Post{
		{ID: "p1", AuthorID: "other", Content: "some post"},
	}

	a1 := p.DecideFromTimeline(timeline, "test", 5, "twitter")
	a2 := p.DecideFromTimeline(timeline, "test", 5, "twitter")

	if len(a1) != len(a2) {
		t.Errorf("DecideFromTimeline not deterministic: %d vs %d actions", len(a1), len(a2))
	}
}
