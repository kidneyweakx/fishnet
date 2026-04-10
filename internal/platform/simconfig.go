package platform

import (
	"context"
	"encoding/json"
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
	Location     string // free-text location from social-media bio
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

// llmAgentResponse is the JSON shape expected from the LLM per-agent batch prompt.
// The "id" field matches the index sent in the request array.
type llmAgentResponse struct {
	ID              int      `json:"id"`
	Stance          string   `json:"stance"`
	SentimentBias   float64  `json:"sentiment_bias"`
	PostsPerHour    float64  `json:"posts_per_hour"`
	CommentsPerHour float64  `json:"comments_per_hour"`
	InfluenceWeight float64  `json:"influence_weight"`

	// Rich persona fields (mirrors MiroFish oasis_profile_generator fields)
	Username     string   `json:"username"`
	RealName     string   `json:"real_name"`
	Profession   string   `json:"profession"`
	Location     string   `json:"location"`  // e.g. "Tokyo, Japan" — used to infer timezone
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

// agentBatchSize is the max number of agents per LLM call.
// ~20 agents fits comfortably within a typical 4096-token context window.
const agentBatchSize = 20

const simConfigSysPrompt = `You are building rich social-media agent personas for a simulation.

Given a JSON array of agents (id, name, type, bio), return a JSON array of persona configs in the SAME ORDER with the SAME length. Each element:
{
  "id": <same integer>,
  "stance": "neutral",
  "sentiment_bias": 0.0,
  "posts_per_hour": 1.0,
  "comments_per_hour": 2.0,
  "influence_weight": 1.0,
  "username": "handle_without_@",
  "real_name": "Display Name",
  "profession": "role or job",
  "location": "City, Country",
  "creativity": 0.5,
  "rationality": 0.5,
  "empathy": 0.5,
  "extraversion": 0.5,
  "openness": 0.5,
  "posting_style": "informative",
  "catchphrases": ["phrase1","phrase2","phrase3"],
  "topics": ["topic1","topic2"]
}
Rules:
- stance: supportive | opposing | neutral | observer (relative to the scenario)
- sentiment_bias: -1.0 to 1.0
- posts_per_hour: 0.1 to 5.0, comments_per_hour: 0.1 to 10.0
- influence_weight: 0.0 to 2.0 (celebrities/orgs should be higher)
- creativity/rationality/empathy/extraversion/openness: 0.0 to 1.0
- posting_style: formal | casual | technical | emotional | analytical | informative | humorous
- catchphrases: 3-5 phrases this agent characteristically uses
- location: realistic city and country that fits the agent's background
- Return ONLY the JSON array, no markdown.`

// GenerateSimConfig generates per-agent simulation config using a single batched LLM
// call per group of agents (≤20 per call). This replaces the old 1-call-per-agent
// approach, reducing total LLM calls by ~20× and improving consistency since the
// LLM sees all agents at once and can assign complementary roles.
func GenerateSimConfig(
	ctx context.Context,
	llmClient *llm.Client,
	nodes []db.Node,
	scenario, timezone string,
	concurrency int,
) (*SimConfig, error) {
	cfg := &SimConfig{
		Scenario:       scenario,
		TimeZone:       timezone,
		ActivityByHour: defaultActivityByHour(timezone),
		AgentCfgs:      make([]AgentSimConfig, len(nodes)),
	}

	// Fill all slots with defaults first so any failed batch still has safe values.
	for i, n := range nodes {
		cfg.AgentCfgs[i] = agentSimConfigDefaults(n.ID)
	}

	// Split nodes into batches of agentBatchSize and run them concurrently.
	type batchJob struct {
		startIdx int
		nodes    []db.Node
	}
	var jobs []batchJob
	for i := 0; i < len(nodes); i += agentBatchSize {
		end := i + agentBatchSize
		if end > len(nodes) {
			end = len(nodes)
		}
		jobs = append(jobs, batchJob{startIdx: i, nodes: nodes[i:end]})
	}

	if concurrency <= 0 {
		concurrency = 4
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, job := range jobs {
		wg.Add(1)
		go func(j batchJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			results := generateAgentBatch(ctx, llmClient, j.nodes, scenario)

			mu.Lock()
			for localIdx, ac := range results {
				cfg.AgentCfgs[j.startIdx+localIdx] = ac
			}
			mu.Unlock()
		}(job)
	}
	wg.Wait()

	// Generate EventConfig (seed posts + hot topics) — best effort, never fatal.
	cfg.EventConfig = generateEventConfig(ctx, llmClient, scenario, cfg.AgentCfgs)

	return cfg, nil
}

// generateAgentBatch sends one LLM call for a slice of nodes (≤agentBatchSize)
// and returns AgentSimConfig for each, in the same order.
// Falls back to defaults for any agent the LLM omits or garbles.
func generateAgentBatch(
	ctx context.Context,
	llmClient *llm.Client,
	nodes []db.Node,
	scenario string,
) []AgentSimConfig {
	type reqItem struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
		Bio  string `json:"bio,omitempty"`
	}

	// Build request array
	reqs := make([]reqItem, len(nodes))
	for i, n := range nodes {
		bio := n.Summary
		if len([]rune(bio)) > 120 {
			bio = string([]rune(bio)[:120]) + "…"
		}
		reqs[i] = reqItem{ID: i, Name: n.Name, Type: n.Type, Bio: bio}
	}

	reqJSON, err := json.Marshal(reqs)
	if err != nil {
		return defaultsForNodes(nodes)
	}

	userPrompt := fmt.Sprintf("Scenario: %s\n\nAgents:\n%s", truncateScenario(scenario), string(reqJSON))

	var responses []llmAgentResponse
	if err := llmClient.JSON(ctx, simConfigSysPrompt, userPrompt, &responses); err != nil || len(responses) == 0 {
		return defaultsForNodes(nodes)
	}

	// Index responses by their "id" field for robust matching (LLM may reorder).
	byID := make(map[int]llmAgentResponse, len(responses))
	for _, r := range responses {
		byID[r.ID] = r
	}

	results := make([]AgentSimConfig, len(nodes))
	for i, n := range nodes {
		def := agentSimConfigDefaults(n.ID)
		resp, ok := byID[i]
		if !ok {
			results[i] = def
			continue
		}
		results[i] = AgentSimConfig{
			AgentID:         n.ID,
			Stance:          validateStance(resp.Stance),
			SentimentBias:   clampSentimentBias(resp.SentimentBias),
			PostsPerHour:    clampPositive(resp.PostsPerHour, def.PostsPerHour),
			CommentsPerHour: clampPositive(resp.CommentsPerHour, def.CommentsPerHour),
			ActiveHours:     def.ActiveHours,
			InfluenceWeight: clampInfluenceWeight(resp.InfluenceWeight),
			Username:        resp.Username,
			RealName:        resp.RealName,
			Profession:      resp.Profession,
			Location:        resp.Location,
			Creativity:      clampUnit(resp.Creativity),
			Rationality:     clampUnit(resp.Rationality),
			Empathy:         clampUnit(resp.Empathy),
			Extraversion:    clampUnit(resp.Extraversion),
			Openness:        clampUnit(resp.Openness),
			PostingStyle:    validatePostingStyle(resp.PostingStyle),
			Catchphrases:    resp.Catchphrases,
			Topics:          resp.Topics,
		}
	}
	return results
}

// defaultsForNodes returns default AgentSimConfig for a slice of nodes.
func defaultsForNodes(nodes []db.Node) []AgentSimConfig {
	out := make([]AgentSimConfig, len(nodes))
	for i, n := range nodes {
		out[i] = agentSimConfigDefaults(n.ID)
	}
	return out
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
		if ac.Location != "" {
			p.Location = ac.Location
		}
		// Infer timezone from location (no LLM — local lookup table)
		if p.Location != "" && (p.Timezone == "" || p.Timezone == "UTC") {
			p.Timezone = InferTimezone(p.Location)
		}
	}
}

// PersistPersonalityAttrs writes sim-generated personality values back to node.Attributes
// in the database so the TUI and web views can display real values instead of defaults.
// All numeric fields are stored as float64; arrays as JSON arrays — no stringified values.
func PersistPersonalityAttrs(database *db.DB, personalities []*Personality) {
	for _, p := range personalities {
		if p == nil {
			continue
		}
		// Read existing attrs to preserve extraction metadata (e.g. "extracted": true).
		node, err := database.GetNode(p.AgentID)
		if err != nil {
			continue
		}
		attrs := make(map[string]interface{})
		if node.Attributes != "" && node.Attributes != "{}" {
			_ = json.Unmarshal([]byte(node.Attributes), &attrs)
		}

		// Personality fields — stored with clean types for easy JSON parsing.
		attrs["has_personality"]  = true
		attrs["stance"]           = p.Stance
		attrs["activity_level"]   = p.ActivityLevel
		attrs["sentiment_bias"]   = p.SentimentBias
		attrs["influence_weight"] = p.InfluenceWeight
		attrs["originality"]      = p.Originality
		attrs["reactivity"]       = p.Reactivity
		attrs["creativity"]       = p.Creativity
		attrs["rationality"]      = p.Rationality
		attrs["empathy"]          = p.Empathy
		attrs["extraversion"]     = p.Extraversion
		attrs["openness"]         = p.Openness
		attrs["posts_per_hour"]   = p.PostsPerHour
		attrs["comments_per_hour"] = p.CommentsPerHour
		if p.Profession != "" {
			attrs["profession"] = p.Profession
		}
		if p.Username != "" {
			attrs["username"] = p.Username
		}
		if len(p.Interests) > 0 {
			attrs["interests"] = p.Interests // []string → JSON array
		}
		if len(p.Catchphrases) > 0 {
			attrs["catchphrases"] = p.Catchphrases // []string → JSON array
		}
		if p.Location != "" {
			attrs["location"] = p.Location
		}
		if p.Timezone != "" {
			attrs["timezone"] = p.Timezone
		}

		raw, err := json.Marshal(attrs)
		if err != nil {
			continue
		}
		_ = database.UpdateNodeAttributes(p.AgentID, string(raw))
	}
}
