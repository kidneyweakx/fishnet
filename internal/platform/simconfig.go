package platform

import (
	"context"
	"fmt"
	"sync"

	"fishnet/internal/db"
	"fishnet/internal/llm"
)

// ─── SimConfig ────────────────────────────────────────────────────────────────

// EventConfig holds LLM-generated seeding content for the start of a simulation.
type EventConfig struct {
	SeedPosts          []SeedPost `json:"seed_posts"`           // initial posts injected before round 1
	HotTopics          []string   `json:"hot_topics"`           // trending keywords for round 1
	NarrativeDirection string     `json:"narrative_direction"`  // overall narrative goal
}

// SeedPost is an initial post injected into the simulation before round 1.
type SeedPost struct {
	AgentID  string `json:"agent_id"`  // which agent "wrote" this seed post
	Content  string `json:"content"`   // post text
	Platform string `json:"platform"`  // "twitter" or "reddit"
}

// SimConfig is the full simulation configuration for all agents.
type SimConfig struct {
	Scenario   string
	MaxRounds  int
	TimeZone   string // "Asia/Taipei" | "America/New_York" | "UTC" etc.
	AgentCfgs  []AgentSimConfig
	// Timezone activity multipliers (CHINA_TIMEZONE_CONFIG equivalent)
	ActivityByHour [24]float64
	// EventConfig holds optional LLM-generated seeding content (seed posts, hot topics).
	EventConfig *EventConfig
}

// AgentSimConfig is per-agent enriched config from LLM.
type AgentSimConfig struct {
	AgentID         string
	Stance          string
	SentimentBias   float64
	PostsPerHour    float64
	CommentsPerHour float64
	ActiveHours     []int
	InfluenceWeight float64

	// Rich persona fields (mirrors MiroFish oasis_profile_generator fields)
	Username     string
	RealName     string
	Profession   string
	Creativity   float64
	Rationality  float64
	Empathy      float64
	Extraversion float64
	Openness     float64
	PostingStyle string
	Catchphrases []string
	Topics       []string
}

// ─── Activity Patterns ────────────────────────────────────────────────────────

// defaultActivityByHour returns an ActivityByHour array based on the timezone.
// Currently supports Asia/Taipei (and similar China-region patterns) as the
// richly-specified default; all other timezones fall back to a generic pattern.
func defaultActivityByHour(timezone string) [24]float64 {
	switch timezone {
	case "Asia/Taipei", "Asia/Shanghai", "Asia/Hong_Kong", "Asia/Singapore":
		// Based on MiroFish's China pattern:
		//   hours 0-5:   0.05 (dead hours)
		//   hours 6-8:   0.40 (morning)
		//   hours 9-18:  0.70 (work hours)
		//   hours 19-22: 1.50 (peak evening)
		//   hour 23:     0.50 (night)
		return [24]float64{
			0.05, 0.05, 0.05, 0.05, 0.05, 0.05, // 0-5
			0.40, 0.40, 0.40, // 6-8
			0.70, 0.70, 0.70, 0.70, 0.70, 0.70, 0.70, 0.70, 0.70, 0.70, // 9-18
			1.50, 1.50, 1.50, 1.50, // 19-22
			0.50, // 23
		}
	case "America/New_York", "America/Chicago", "America/Denver", "America/Los_Angeles":
		// Generic US pattern
		return [24]float64{
			0.05, 0.05, 0.05, 0.05, 0.05, 0.10, // 0-5
			0.30, 0.50, 0.70, 0.80, 0.80, 0.80, // 6-11
			0.70, 0.70, 0.80, 0.80, 0.70, 0.70, // 12-17
			1.20, 1.50, 1.50, 1.20, 0.80, 0.40, // 18-23
		}
	default:
		// Generic UTC / unknown: moderate daytime activity, quiet at night
		return [24]float64{
			0.05, 0.05, 0.05, 0.05, 0.05, 0.10, // 0-5
			0.30, 0.50, 0.70, 0.80, 0.80, 0.80, // 6-11
			0.70, 0.70, 0.80, 0.80, 0.70, 0.70, // 12-17
			1.00, 1.20, 1.20, 1.00, 0.70, 0.30, // 18-23
		}
	}
}

// ─── GenerateSimConfig ────────────────────────────────────────────────────────

// llmAgentResponse is the JSON shape expected from the LLM per-agent prompt.
type llmAgentResponse struct {
	Stance          string   `json:"stance"`
	SentimentBias   float64  `json:"sentiment_bias"`
	PostsPerHour    float64  `json:"posts_per_hour"`
	CommentsPerHour float64  `json:"comments_per_hour"`
	InfluenceWeight float64  `json:"influence_weight"`

	// Rich persona fields (mirrors MiroFish oasis_profile_generator fields)
	Username     string   `json:"username"`
	RealName     string   `json:"real_name"`
	Profession   string   `json:"profession"`
	Creativity   float64  `json:"creativity"`
	Rationality  float64  `json:"rationality"`
	Empathy      float64  `json:"empathy"`
	Extraversion float64  `json:"extraversion"`
	Openness     float64  `json:"openness"`
	PostingStyle string   `json:"posting_style"`
	Catchphrases []string `json:"catchphrases"`
	Topics       []string `json:"topics"`
}

// agentSimConfigDefaults returns safe default AgentSimConfig values for a node.
func agentSimConfigDefaults(nodeID string) AgentSimConfig {
	return AgentSimConfig{
		AgentID:         nodeID,
		Stance:          "neutral",
		SentimentBias:   0.0,
		PostsPerHour:    1.0,
		CommentsPerHour: 2.0,
		ActiveHours:     []int{8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22},
		InfluenceWeight: 1.0,
	}
}

// validateStance ensures stance is one of the accepted values.
func validateStance(s string) string {
	switch s {
	case "supportive", "opposing", "neutral", "observer":
		return s
	default:
		return "neutral"
	}
}

// clampSentimentBias clamps a value to [-1.0, 1.0].
func clampSentimentBias(v float64) float64 {
	if v < -1.0 {
		return -1.0
	}
	if v > 1.0 {
		return 1.0
	}
	return v
}

// clampInfluenceWeight clamps a value to [0.0, 2.0].
func clampInfluenceWeight(v float64) float64 {
	if v < 0.0 {
		return 0.0
	}
	if v > 2.0 {
		return 2.0
	}
	return v
}

// GenerateSimConfig generates per-agent simulation config using the LLM concurrently.
// For each node, a compact prompt is sent: "name|type|scenario → stance, sentiment_bias, activity level".
// Falls back to defaults if the LLM call fails for any individual agent.
func GenerateSimConfig(
	ctx context.Context,
	llmClient *llm.Client,
	nodes []db.Node,
	scenario, timezone string,
	concurrency int,
) (*SimConfig, error) {
	if concurrency <= 0 {
		concurrency = 6
	}

	cfg := &SimConfig{
		Scenario:       scenario,
		TimeZone:       timezone,
		ActivityByHour: defaultActivityByHour(timezone),
		AgentCfgs:      make([]AgentSimConfig, len(nodes)),
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	const sysPrompt = `Given "name|type|scenario", infer a rich agent persona and activity config for a social simulation.
Return JSON only (no markdown):
{
  "stance":"neutral",
  "sentiment_bias":0.0,
  "posts_per_hour":1.0,
  "comments_per_hour":2.0,
  "influence_weight":1.0,
  "username":"handle_123",
  "real_name":"Display Name",
  "profession":"role description",
  "creativity":0.5,
  "rationality":0.5,
  "empathy":0.5,
  "extraversion":0.5,
  "openness":0.5,
  "posting_style":"informative",
  "catchphrases":["phrase one","phrase two","phrase three"],
  "topics":["topic1","topic2"]
}
stance: one of supportive, opposing, neutral, observer
sentiment_bias: -1.0 (very negative) to 1.0 (very positive)
posts_per_hour: 0.1 to 5.0
comments_per_hour: 0.1 to 10.0
influence_weight: 0.0 to 2.0
creativity/rationality/empathy/extraversion/openness: 0.0 to 1.0
posting_style: one of formal, casual, technical, emotional, analytical, informative, humorous
catchphrases: 3-5 characteristic phrases this agent uses
topics: list of content topics this agent favors`

	for i, node := range nodes {
		wg.Add(1)
		go func(idx int, n db.Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			def := agentSimConfigDefaults(n.ID)

			prompt := fmt.Sprintf("%s|%s|scenario: %s", n.Name, n.Type, truncateScenario(scenario))
			var resp llmAgentResponse
			err := llmClient.JSON(ctx, sysPrompt, prompt, &resp)
			if err != nil {
				cfg.AgentCfgs[idx] = def
				return
			}

			cfg.AgentCfgs[idx] = AgentSimConfig{
				AgentID:         n.ID,
				Stance:          validateStance(resp.Stance),
				SentimentBias:   clampSentimentBias(resp.SentimentBias),
				PostsPerHour:    clampPositive(resp.PostsPerHour, def.PostsPerHour),
				CommentsPerHour: clampPositive(resp.CommentsPerHour, def.CommentsPerHour),
				ActiveHours:     def.ActiveHours, // LLM doesn't return this; keep default
				InfluenceWeight: clampInfluenceWeight(resp.InfluenceWeight),

				// Rich persona fields
				Username:     resp.Username,
				RealName:     resp.RealName,
				Profession:   resp.Profession,
				Creativity:   clampUnit(resp.Creativity),
				Rationality:  clampUnit(resp.Rationality),
				Empathy:      clampUnit(resp.Empathy),
				Extraversion: clampUnit(resp.Extraversion),
				Openness:     clampUnit(resp.Openness),
				PostingStyle: validatePostingStyle(resp.PostingStyle),
				Catchphrases: resp.Catchphrases,
				Topics:       resp.Topics,
			}
		}(i, node)
	}
	wg.Wait()

	// Generate EventConfig (seed posts + hot topics) — best effort, never fatal.
	cfg.EventConfig = generateEventConfig(ctx, llmClient, scenario, cfg.AgentCfgs)

	return cfg, nil
}

// llmEventConfigResponse is the JSON shape expected from the LLM event-config prompt.
type llmEventConfigResponse struct {
	SeedPosts          []SeedPost `json:"seed_posts"`
	HotTopics          []string   `json:"hot_topics"`
	NarrativeDirection string     `json:"narrative_direction"`
}

// generateEventConfig asks the LLM to produce 3-5 seed posts and 5 hot topics
// relevant to the scenario. Falls back to an empty EventConfig if anything fails.
func generateEventConfig(
	ctx context.Context,
	llmClient *llm.Client,
	scenario string,
	agents []AgentSimConfig,
) *EventConfig {
	if llmClient == nil || len(agents) == 0 {
		return &EventConfig{}
	}

	// Build a compact list of agent IDs to reference in the prompt.
	agentSummaries := ""
	for i, a := range agents {
		if i >= 10 { // cap at 10 to keep prompt concise
			break
		}
		name := a.Username
		if name == "" {
			name = a.AgentID
		}
		agentSummaries += fmt.Sprintf("  - agent_id: %q, name: %q, stance: %q\n", a.AgentID, name, a.Stance)
	}

	const sysPrompt = `You generate seeding content for a social media simulation.
Given a scenario and a list of agents, produce 3-5 seed posts that would realistically appear
at the very start of the simulation (before any agent acts), plus 5 hot topic keywords and an
overall narrative direction.

Return JSON only (no markdown, no extra text):
{
  "seed_posts": [
    {"agent_id": "...", "content": "...", "platform": "twitter"},
    {"agent_id": "...", "content": "...", "platform": "reddit"}
  ],
  "hot_topics": ["topic1", "topic2", "topic3", "topic4", "topic5"],
  "narrative_direction": "one sentence describing the overall narrative arc"
}

Rules:
- Each seed post must reference one of the provided agent_ids exactly.
- platform must be "twitter" or "reddit".
- hot_topics should be concise keywords (1-3 words each).
- Keep post content under 280 characters.`

	userPrompt := fmt.Sprintf("Scenario: %s\n\nAgents:\n%s", truncateScenario(scenario), agentSummaries)

	var resp llmEventConfigResponse
	err := llmClient.JSON(ctx, sysPrompt, userPrompt, &resp)
	if err != nil {
		// Best-effort fallback: return empty EventConfig rather than propagating error.
		return &EventConfig{}
	}

	// Validate and filter seed posts.
	agentIDSet := make(map[string]bool, len(agents))
	for _, a := range agents {
		agentIDSet[a.AgentID] = true
	}

	filtered := resp.SeedPosts[:0]
	for _, sp := range resp.SeedPosts {
		if !agentIDSet[sp.AgentID] {
			continue // skip posts referencing unknown agents
		}
		if sp.Platform != "twitter" && sp.Platform != "reddit" {
			sp.Platform = "twitter" // normalise unknown platforms
		}
		if sp.Content == "" {
			continue
		}
		filtered = append(filtered, sp)
	}

	return &EventConfig{
		SeedPosts:          filtered,
		HotTopics:          resp.HotTopics,
		NarrativeDirection: resp.NarrativeDirection,
	}
}

// clampUnit clamps a value to [0.0, 1.0].
func clampUnit(v float64) float64 {
	if v < 0.0 {
		return 0.0
	}
	if v > 1.0 {
		return 1.0
	}
	return v
}

// validatePostingStyle ensures posting_style is one of the accepted values.
// Returns "informative" as the safe fallback.
func validatePostingStyle(s string) string {
	switch s {
	case "formal", "casual", "technical", "emotional", "analytical", "informative", "humorous":
		return s
	default:
		return "informative"
	}
}

// clampPositive returns v if v > 0, otherwise returns fallback.
func clampPositive(v, fallback float64) float64 {
	if v <= 0 {
		return fallback
	}
	return v
}

// truncateScenario trims the scenario string to 80 runes.
func truncateScenario(s string) string {
	runes := []rune(s)
	if len(runes) <= 80 {
		return s
	}
	return string(runes[:80]) + "…"
}

// ─── ApplySimConfig ───────────────────────────────────────────────────────────

// ApplySimConfig matches AgentSimConfig entries to Personality objects by AgentID
// and applies all SimConfig fields.
func ApplySimConfig(personalities []*Personality, cfg *SimConfig) {
	// Build a lookup map for O(1) access
	cfgByID := make(map[string]*AgentSimConfig, len(cfg.AgentCfgs))
	for i := range cfg.AgentCfgs {
		ac := &cfg.AgentCfgs[i]
		cfgByID[ac.AgentID] = ac
	}

	for _, p := range personalities {
		if p == nil {
			continue
		}
		ac, ok := cfgByID[p.AgentID]
		if !ok {
			continue
		}
		p.Stance = ac.Stance
		p.SentimentBias = ac.SentimentBias
		p.PostsPerHour = ac.PostsPerHour
		p.CommentsPerHour = ac.CommentsPerHour
		if len(ac.ActiveHours) > 0 {
			p.ActiveHours = ac.ActiveHours
		}
		p.InfluenceWeight = ac.InfluenceWeight

		// Apply rich persona fields when provided by LLM
		if ac.Username != "" {
			p.Username = ac.Username
		}
		if ac.RealName != "" {
			p.RealName = ac.RealName
		}
		if ac.Profession != "" {
			p.Profession = ac.Profession
		}
		if ac.Creativity > 0 {
			p.Creativity = ac.Creativity
		}
		if ac.Rationality > 0 {
			p.Rationality = ac.Rationality
		}
		if ac.Empathy > 0 {
			p.Empathy = ac.Empathy
		}
		if ac.Extraversion > 0 {
			p.Extraversion = ac.Extraversion
		}
		if ac.Openness > 0 {
			p.Openness = ac.Openness
		}
		if ac.PostingStyle != "" {
			p.PostStyle = ac.PostingStyle
		}
		if len(ac.Catchphrases) > 0 {
			p.Catchphrases = ac.Catchphrases
		}
		if len(ac.Topics) > 0 {
			p.Interests = ac.Topics
		}
	}
}
