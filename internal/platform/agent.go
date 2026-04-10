package platform

import (
	"math/rand"

	"fishnet/internal/db"
)

// ─── Personality ──────────────────────────────────────────────────────────────

// Personality captures an agent's behavioral profile.
// Generated ONCE by LLM at simulation start — never called per round.
type Personality struct {
	AgentID   string   `json:"agent_id"`
	Name      string   `json:"name"`
	NodeType  string   `json:"node_type"`
	Bio       string   `json:"bio"`
	Interests []string `json:"interests"` // content topics this agent favors
	PostStyle string   `json:"post_style"` // "informative" | "emotional" | "analytical" | "humorous" | "formal" | "casual" | "technical"

	// Rich persona fields (mirrors MiroFish OasisAgentProfile)
	Username     string   `json:"username"`     // social-media handle
	RealName     string   `json:"real_name"`    // display name / real name
	Profession   string   `json:"profession"`   // job / role description
	Catchphrases []string `json:"catchphrases"` // 3-5 characteristic phrases used in posts

	// Big-Five-inspired personality traits [0,1]
	Creativity   float64 `json:"creativity"`   // openness to novel ideas
	Rationality  float64 `json:"rationality"`  // preference for logical over emotional reasoning
	Empathy      float64 `json:"empathy"`      // responsiveness to others' emotions
	Extraversion float64 `json:"extraversion"` // social energy / talkativeness
	Openness     float64 `json:"openness"`     // willingness to engage with opposing views

	// Behavioral weights [0,1]
	ActivityLevel float64 `json:"activity_level"` // probability of taking any action this round
	Reactivity    float64 `json:"reactivity"`     // tendency to respond to others' content
	Originality   float64 `json:"originality"`    // tendency to create original posts
	Positivity    float64 `json:"positivity"`     // sentiment bias (high = positive)
	Verbosity     float64 `json:"verbosity"`      // post length preference
	Leadership    float64 `json:"leadership"`     // influence / follower draw

	// Stance toward the scenario topic
	Stance        string  `json:"stance"`         // "supportive" | "opposing" | "neutral" | "observer"
	SentimentBias float64 `json:"sentiment_bias"` // -1.0 (very negative) to 1.0 (very positive)

	// Activity timing
	PostsPerHour    float64 `json:"posts_per_hour"`    // expected posts per hour
	CommentsPerHour float64 `json:"comments_per_hour"` // expected comments per hour
	ActiveHours     []int   `json:"active_hours"`      // 0-23, which hours this agent is active (local timezone)

	// Response behavior
	ResponseDelayMin int     `json:"response_delay_min"` // min delay in sim-seconds before reacting
	ResponseDelayMax int     `json:"response_delay_max"` // max delay in sim-seconds before reacting
	InfluenceWeight  float64 `json:"influence_weight"`   // 0-2.0, how visible this agent's posts are

	// Graph community this agent belongs to (from Louvain detection, -1 if none)
	CommunityID      int    `json:"community_id"`
	CommunitySummary string `json:"community_summary"`
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

		ActivityLevel: 0.55 + rng.Float64()*0.35,
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
		CommunityID:      -1,
	}
}

// ─── Decision Engine ──────────────────────────────────────────────────────────

// PlannedAction is what an agent decided to do (before execution).
type PlannedAction struct {
	Type     string // one of Act* constants from state.go
	PostID   string // target post (for reactions)
	TargetID string // target user (for FOLLOW/MUTE/SEARCH_USER)
	Topic    string // content hint for LLM generation
	Query    string // search query (SEARCH_POSTS / SEARCH_USER)
	NeedLLM  bool   // whether content generation is needed
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
	return p.decideWithTimeline(timeline, state, scenario, round, state.Platform)
}

// DecideFromTimeline returns what this agent will do given a pre-built timeline slice.
// This allows the caller to supply a weighted/custom timeline.
// platName should be "twitter" or "reddit"; it controls which action vocabulary is used.
// For follow actions the state is needed to pick a random post; pass nil to skip follows.
func (p *Personality) DecideFromTimeline(timeline []*Post, scenario string, round int, platName string) []PlannedAction {
	return p.decideWithTimeline(timeline, nil, scenario, round, platName)
}

// decideWithTimeline is the shared implementation.
// state may be nil; if so, follow/mute/search actions are skipped.
func (p *Personality) decideWithTimeline(timeline []*Post, state *State, scenario string, round int, platName string) []PlannedAction {
	rng := rand.New(rand.NewSource(int64(round)*7919 + hashStr(p.AgentID)))
	isReddit := platName == "reddit"

	// Check active hours: map round to hour of day
	activityMod := 1.0
	currentHour := round % 24
	if !p.isActiveHour(currentHour) {
		activityMod = 0.70
	}

	toneHint := p.stanceToneHint()

	var actions []PlannedAction

	// ── Passive actions: REFRESH / TREND ─────────────────────────────────
	// Agents sometimes just browse without interacting
	if rng.Float64() < 0.12 {
		actions = append(actions, PlannedAction{Type: ActRefresh})
	}
	if rng.Float64() < 0.08 {
		actions = append(actions, PlannedAction{Type: ActTrend})
	}

	// ── React to timeline posts ──────────────────────────────────────────
	for _, post := range timeline {
		r := rng.Float64()

		// Adjust reactivity based on stance
		reactivityMod := p.Reactivity * activityMod
		if p.Stance == "opposing" {
			reactivityMod *= 0.7
		} else if p.Stance == "supportive" {
			reactivityMod *= 1.2
			if reactivityMod > 1.0 {
				reactivityMod = 1.0
			}
		}

		switch {
		case r < reactivityMod*0.35:
			if p.Stance == "opposing" && isReddit {
				// Opposing agents on Reddit dislike instead of like
				actions = append(actions, PlannedAction{Type: ActDislikePost, PostID: post.ID})
			} else if p.Stance != "opposing" {
				actions = append(actions, PlannedAction{Type: ActLikePost, PostID: post.ID})
			}

		case r < reactivityMod*0.48:
			actions = append(actions, PlannedAction{Type: ActRepost, PostID: post.ID})

		case r < reactivityMod*0.62:
			// CREATE_COMMENT (Reddit) or QUOTE_POST (Twitter) — both need LLM
			if isReddit {
				actions = append(actions, PlannedAction{
					Type: ActCreateComment, PostID: post.ID, NeedLLM: true,
					Topic: "reply to: " + clip(post.Content, 80) + toneHint,
				})
			} else {
				actions = append(actions, PlannedAction{
					Type: ActQuotePost, PostID: post.ID, NeedLLM: true,
					Topic: "quote this: " + clip(post.Content, 80) + toneHint,
				})
			}

		case r < reactivityMod*0.70 && isReddit && post.Comments > 0:
			// Like or dislike a comment (Reddit only)
			if p.Stance == "opposing" {
				actions = append(actions, PlannedAction{Type: ActDislikeComment, PostID: post.ID})
			} else {
				actions = append(actions, PlannedAction{Type: ActLikeComment, PostID: post.ID})
			}
		}
	}

	// ── Original post ────────────────────────────────────────────────────
	createProb := p.Originality * 0.45 * activityMod
	if p.Stance == "observer" {
		createProb *= 0.30
	}
	if rng.Float64() < createProb {
		topic := scenario
		if len(p.Interests) > 0 && rng.Float64() < 0.35 {
			topic = p.Interests[rng.Intn(len(p.Interests))]
		}
		topic += toneHint
		actions = append(actions, PlannedAction{
			Type: ActCreatePost, NeedLLM: true, Topic: topic,
		})
	}

	// ── Search actions (Reddit-only, curiosity-driven) ───────────────────
	if isReddit && state != nil {
		if rng.Float64() < p.Openness*0.10 {
			q := scenario
			if len(p.Interests) > 0 {
				q = p.Interests[rng.Intn(len(p.Interests))]
			}
			actions = append(actions, PlannedAction{Type: ActSearchPosts, Query: q})
		}
		if rng.Float64() < 0.03 {
			actions = append(actions, PlannedAction{Type: ActSearchUser, Query: scenario})
		}
	}

	// ── Follow / Mute (social-graph actions) ─────────────────────────────
	if state != nil {
		if rng.Float64() < 0.04 {
			if post := state.RandomPost(rng); post != nil && post.AuthorID != p.AgentID {
				actions = append(actions, PlannedAction{Type: ActFollow, TargetID: post.AuthorID})
			}
		}
		// Mute: opposing agents occasionally mute supporters (Reddit only)
		if isReddit && p.Stance == "opposing" && rng.Float64() < 0.02 {
			if post := state.RandomPost(rng); post != nil && post.AuthorID != p.AgentID {
				actions = append(actions, PlannedAction{Type: ActMute, TargetID: post.AuthorID})
			}
		}
	}

	// ── Cap actions per round ────────────────────────────────────────────
	if len(actions) > 6 {
		actions = actions[:6]
	}

	// If nothing was generated, emit explicit DO_NOTHING
	if len(actions) == 0 {
		actions = append(actions, PlannedAction{Type: ActDoNothing})
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
