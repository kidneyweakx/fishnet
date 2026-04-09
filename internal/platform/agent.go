package platform

import (
	"math/rand"

	"fishnet/internal/db"
)

// ─── Personality ──────────────────────────────────────────────────────────────

// Personality captures an agent's behavioral profile.
// Generated ONCE by LLM at simulation start — never called per round.
type Personality struct {
	AgentID   string
	Name      string
	NodeType  string
	Bio       string
	Interests []string // content topics this agent favors
	PostStyle string   // "informative" | "emotional" | "analytical" | "humorous" | "formal" | "casual" | "technical"

	// Rich persona fields (mirrors MiroFish OasisAgentProfile)
	Username   string   // social-media handle
	RealName   string   // display name / real name
	Profession string   // job / role description
	Catchphrases []string // 3-5 characteristic phrases used in posts

	// Big-Five-inspired personality traits [0,1]
	Creativity   float64 // openness to novel ideas
	Rationality  float64 // preference for logical over emotional reasoning
	Empathy      float64 // responsiveness to others' emotions
	Extraversion float64 // social energy / talkativeness
	Openness     float64 // willingness to engage with opposing views

	// Behavioral weights [0,1]
	ActivityLevel float64 // probability of taking any action this round
	Reactivity    float64 // tendency to respond to others' content
	Originality   float64 // tendency to create original posts
	Positivity    float64 // sentiment bias (high = positive)
	Verbosity     float64 // post length preference
	Leadership    float64 // influence / follower draw

	// Stance toward the scenario topic
	Stance        string  // "supportive" | "opposing" | "neutral" | "observer"
	SentimentBias float64 // -1.0 (very negative) to 1.0 (very positive)

	// Activity timing
	PostsPerHour    float64 // expected posts per hour
	CommentsPerHour float64 // expected comments per hour
	ActiveHours     []int   // 0-23, which hours this agent is active (local timezone)

	// Response behavior
	ResponseDelayMin int     // min delay in sim-seconds before reacting
	ResponseDelayMax int     // max delay in sim-seconds before reacting
	InfluenceWeight  float64 // 0-2.0, how visible this agent's posts are
}

// FromNode creates a default personality from a graph node (no LLM).
// Used as fallback or when LLM enrichment is disabled.
func FromNode(n db.Node, idx int) *Personality {
	rng := rand.New(rand.NewSource(int64(idx)*13337 + hashStr(n.ID)))
	return &Personality{
		AgentID:   n.ID,
		Name:      n.Name,
		NodeType:  n.Type,
		Bio:       n.Summary,
		Interests: []string{n.Type},
		PostStyle: "informative",

		// Rich persona defaults derived deterministically from the node
		Username:     "",         // populated by LLM enrichment; empty until then
		RealName:     n.Name,
		Profession:   n.Type,
		Catchphrases: []string{}, // populated by LLM enrichment

		// Big-Five-inspired traits — seeded random defaults
		Creativity:   0.20 + rng.Float64()*0.60,
		Rationality:  0.20 + rng.Float64()*0.60,
		Empathy:      0.20 + rng.Float64()*0.60,
		Extraversion: 0.20 + rng.Float64()*0.60,
		Openness:     0.20 + rng.Float64()*0.60,

		ActivityLevel: 0.25 + rng.Float64()*0.45,
		Reactivity:    0.30 + rng.Float64()*0.40,
		Originality:   0.20 + rng.Float64()*0.35,
		Positivity:    0.30 + rng.Float64()*0.40,
		Verbosity:     0.30 + rng.Float64()*0.40,
		Leadership:    rng.Float64(),

		Stance:           "neutral",
		SentimentBias:    rng.Float64()*0.6 - 0.3, // -0.3 to 0.3
		PostsPerHour:     0.5 + rng.Float64()*2.0,
		CommentsPerHour:  1.0 + rng.Float64()*3.0,
		ActiveHours:      []int{8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22},
		ResponseDelayMin: 5,
		ResponseDelayMax: 60,
		InfluenceWeight:  0.5 + rng.Float64()*1.5,
	}
}

// ─── Decision Engine ──────────────────────────────────────────────────────────

// PlannedAction is what an agent decided to do (before execution).
type PlannedAction struct {
	Type    string // CREATE_POST | LIKE_POST | REPOST | COMMENT | QUOTE_POST | FOLLOW
	PostID  string // target post (for reactions)
	Topic   string // content hint for LLM generation
	NeedLLM bool   // whether content generation is needed
}

// isActiveHour returns true if the given hour (0-23) is in the agent's ActiveHours list.
func (p *Personality) isActiveHour(hour int) bool {
	for _, h := range p.ActiveHours {
		if h == hour {
			return true
		}
	}
	return false
}

// stanceToneHint returns a prompt annotation string based on stance and sentimentBias.
func (p *Personality) stanceToneHint() string {
	hint := ""
	switch p.Stance {
	case "supportive":
		hint = " [supportive tone]"
	case "opposing":
		hint = " [critical tone]"
	}
	if p.SentimentBias > 0.3 {
		hint += " [positive sentiment]"
	} else if p.SentimentBias < -0.3 {
		hint += " [negative sentiment]"
	}
	return hint
}

// Decide returns what this agent will do in a given round using the platform state.
// Pure math — zero LLM calls. Reproducible given the same round+agent seed.
func (p *Personality) Decide(state *State, scenario string, round int) []PlannedAction {
	timeline := state.Timeline(p.AgentID, 10)
	return p.decideWithTimeline(timeline, state, scenario, round)
}

// DecideFromTimeline returns what this agent will do given a pre-built timeline slice.
// This allows the caller to supply a weighted/custom timeline.
// For follow actions the state is needed to pick a random post; pass nil to skip follows.
func (p *Personality) DecideFromTimeline(timeline []*Post, scenario string, round int) []PlannedAction {
	return p.decideWithTimeline(timeline, nil, scenario, round)
}

// decideWithTimeline is the shared implementation.
// state may be nil; if so, follow actions are skipped.
func (p *Personality) decideWithTimeline(timeline []*Post, state *State, scenario string, round int) []PlannedAction {
	rng := rand.New(rand.NewSource(int64(round)*7919 + hashStr(p.AgentID)))

	// Check active hours: map round to hour of day
	currentHour := round % 24
	if !p.isActiveHour(currentHour) {
		// Agents outside their active hours are mostly quiet; small chance they act anyway
		if rng.Float64() > 0.05 {
			return nil
		}
	}

	// Skip inactive rounds
	if rng.Float64() > p.ActivityLevel {
		return nil
	}

	toneHint := p.stanceToneHint()

	var actions []PlannedAction

	// React to timeline posts
	for _, post := range timeline {
		r := rng.Float64()

		// Adjust reactivity based on stance
		reactivityMod := p.Reactivity
		if p.Stance == "opposing" {
			reactivityMod *= 0.7
		} else if p.Stance == "supportive" {
			reactivityMod *= 1.2
			if reactivityMod > 1.0 {
				reactivityMod = 1.0
			}
		}

		switch {
		case r < reactivityMod*0.40:
			// Opposing agents dislike rather than like — model as no-like
			if p.Stance != "opposing" {
				actions = append(actions, PlannedAction{Type: "LIKE_POST", PostID: post.ID})
			}
		case r < reactivityMod*0.55:
			actions = append(actions, PlannedAction{Type: "REPOST", PostID: post.ID})
		case r < reactivityMod*0.20:
			actions = append(actions, PlannedAction{
				Type: "COMMENT", PostID: post.ID, NeedLLM: true,
				Topic: "reply to: " + clip(post.Content, 80) + toneHint,
			})
		case r < reactivityMod*0.08:
			actions = append(actions, PlannedAction{
				Type: "QUOTE_POST", PostID: post.ID, NeedLLM: true,
				Topic: "quote this: " + clip(post.Content, 80) + toneHint,
			})
		}
	}

	// Original post — always scenario-aware, sometimes interest-based
	// Observer agents have 70% reduced CREATE_POST probability
	createProb := p.Originality * 0.45
	if p.Stance == "observer" {
		createProb *= 0.30 // reduce by 70%
	}

	if rng.Float64() < createProb {
		topic := scenario
		if len(p.Interests) > 0 && rng.Float64() < 0.35 {
			topic = p.Interests[rng.Intn(len(p.Interests))]
		}
		topic += toneHint
		actions = append(actions, PlannedAction{
			Type: "CREATE_POST", NeedLLM: true, Topic: topic,
		})
	}

	// Follow a user (rare social action) — requires state
	if state != nil && rng.Float64() < 0.04 {
		if post := state.RandomPost(rng); post != nil && post.AuthorID != p.AgentID {
			actions = append(actions, PlannedAction{Type: "FOLLOW", PostID: post.AuthorID})
		}
	}

	// Limit actions per round to avoid spam
	if len(actions) > 4 {
		actions = actions[:4]
	}
	return actions
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// HashStr is an exported hash function for use by other packages (e.g. sim).
func HashStr(s string) int64 {
	return hashStr(s)
}

func hashStr(s string) int64 {
	var h int64 = 5381
	for _, c := range s {
		h = h*33 + int64(c)
	}
	return h
}

func clip(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
