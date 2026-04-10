package sim

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"fishnet/internal/db"
	"fishnet/internal/llm"
	"fishnet/internal/platform"
)

// ─── Config & Progress ────────────────────────────────────────────────────────

// RoundConfig configures a full multi-round social simulation.
type RoundConfig struct {
	Scenario    string
	MaxRounds   int
	MaxAgents   int
	Platforms   []string // "twitter" | "reddit"
	OutputDir   string   // dir for actions.jsonl; empty = skip
	Concurrency int      // max concurrent LLM content calls (default 6)
	SimConfig   *platform.SimConfig // optional; applied after buildPersonalities

	// EventConfig holds optional seeding content injected before round 1.
	// When nil and SimConfig is set, SimConfig.EventConfig is used automatically.
	EventConfig *platform.EventConfig

	// EnableGraphMemory causes simulation actions to be written back to the
	// knowledge graph as new edges at the end of each round.
	EnableGraphMemory bool

	// Intervention support: if non-nil, events are drained each round and applied.
	InterventionQueue *InterventionQueue

	// CopyReaction support: if non-nil, inject copy and collect reactions at InjectRound.
	CopyReaction *CopyReactionConfig

	// Personalities, when non-nil, skips the LLM personality-building phase
	// and uses these pre-built profiles directly (loaded from a `sim prepare` snapshot).
	Personalities []*platform.Personality

	// NoLLM disables ALL LLM calls:
	//   - Personalities use graph-node defaults (no enrichment)
	//   - Content is generated from local templates
	// Use when you need near-zero token cost.
	NoLLM bool
}

// RoundProgress is one event emitted during simulation.
type RoundProgress struct {
	Round        int
	MaxRounds    int
	Action       platform.Action
	TwitterStat  platform.Stats
	RedditStat   platform.Stats
	Done         bool
	Error        error
	Logs         []string           // non-fatal log messages (e.g. graph memory errors)
	Intervention *InterventionEvent // set if an intervention was applied this round
	Paused       bool               // true when the sim has just been paused; false when resumed
}

// ─── Platform Simulation ──────────────────────────────────────────────────────

// PlatformSim runs a multi-round Twitter + Reddit simulation.
//
// Efficiency model:
//   - Agent personality: LLM call once at startup (concurrent)
//   - Per-round decisions: deterministic math (no LLM)
//   - Content generation: LLM only for CREATE_POST / COMMENT actions
//   - This reduces LLM calls by ~15-20x compared to OASIS
type PlatformSim struct {
	db  *db.DB
	llm *llm.Client
}

func NewPlatformSim(database *db.DB, client *llm.Client) *PlatformSim {
	return &PlatformSim{db: database, llm: client}
}

// Run executes the simulation. Sends progress on progressCh (may be nil).
// Returns when the simulation is complete or ctx is cancelled.
func (ps *PlatformSim) Run(
	ctx context.Context,
	projectID string,
	cfg RoundConfig,
	progressCh chan<- RoundProgress,
) error {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 6
	}
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 10
	}

	// ── Graph memory updater (optional) ──────────────────────────────────────
	var memUpdater *GraphMemoryUpdater
	if cfg.EnableGraphMemory {
		memUpdater = NewGraphMemoryUpdater(ps.db)
	}

	// ── Load nodes ───────────────────────────────────────────────────────────
	nodes, err := ps.db.GetNodes(projectID)
	if err != nil {
		return fmt.Errorf("get nodes: %w", err)
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes found; run: fishnet analyze first")
	}
	if cfg.MaxAgents > 0 && len(nodes) > cfg.MaxAgents {
		nodes = nodes[:cfg.MaxAgents]
	}

	// ── Build personalities (LLM, parallel) ──────────────────────────────────
	// Skip if pre-built personalities were supplied (via `sim prepare`).
	var personalities []*platform.Personality
	if len(cfg.Personalities) > 0 {
		personalities = cfg.Personalities
	} else if cfg.NoLLM {
		personalities = ps.buildPersonalitiesLow(nodes, cfg.SimConfig)
	} else {
		personalities = ps.buildPersonalities(ctx, nodes, cfg.Scenario, cfg.Concurrency, cfg.SimConfig)
	}

	// ── Build InfluenceWeight lookup map ─────────────────────────────────────
	influenceByID := make(map[string]float64, len(personalities))
	for _, p := range personalities {
		if p != nil {
			influenceByID[p.AgentID] = p.InfluenceWeight
		}
	}

	// ── Init platform states ──────────────────────────────────────────────────
	twState := platform.NewState("twitter")
	rdState := platform.NewState("reddit")

	for i, p := range personalities {
		twState.RegisterUser(&platform.User{
			ID:          p.AgentID,
			Name:        "@" + platform.SafeUsername(p.Name),
			Platform:    "twitter",
			Bio:         p.Bio,
			FollowerCnt: 50 + i*30,
		})
		rdState.RegisterUser(&platform.User{
			ID:       p.AgentID + "_rd",
			Name:     "u/" + platform.SafeUsername(p.Name),
			Platform: "reddit",
			Bio:      p.Bio,
		})
	}

	// ── Open output file ──────────────────────────────────────────────────────
	var outFile *os.File
	if cfg.OutputDir != "" {
		if err := os.MkdirAll(cfg.OutputDir, 0755); err == nil {
			outFile, _ = os.Create(filepath.Join(cfg.OutputDir, "actions.jsonl"))
			if outFile != nil {
				defer outFile.Close()
			}
		}
	}

	// roundActions accumulates actions within a single round for graph memory.
	var roundActions []platform.Action
	var roundActionsMu sync.Mutex

	emit := func(a platform.Action) {
		if outFile != nil {
			outFile.Write(a.MarshalLine())
		}
		if progressCh != nil {
			var logs []string
			if !a.Success && a.Error != "" {
				logs = []string{fmt.Sprintf("[%s/%s] %s: %s", a.Platform, a.AgentName, a.Type, a.Error)}
			}
			progressCh <- RoundProgress{
				Round:       a.Round,
				MaxRounds:   cfg.MaxRounds,
				Action:      a,
				TwitterStat: twState.GetStats(),
				RedditStat:  rdState.GetStats(),
				Logs:        logs,
			}
		}
		if memUpdater != nil && a.AgentName != "" {
			roundActionsMu.Lock()
			roundActions = append(roundActions, a)
			roundActionsMu.Unlock()
		}
	}

	// ── Inject EventConfig seed posts before round 1 ─────────────────────────
	// Prefer an explicitly-set EventConfig on the RoundConfig; fall back to
	// whatever was generated inside SimConfig.
	eventCfg := cfg.EventConfig
	if eventCfg == nil && cfg.SimConfig != nil {
		eventCfg = cfg.SimConfig.EventConfig
	}
	if eventCfg != nil && len(eventCfg.SeedPosts) > 0 {
		// Build a lookup: agentID → personality name (for AuthorName).
		nameByID := make(map[string]string, len(personalities))
		for _, p := range personalities {
			if p != nil {
				nameByID[p.AgentID] = p.Name
			}
		}
		injected := 0
		for _, sp := range eventCfg.SeedPosts {
			authorName := nameByID[sp.AgentID]
			if authorName == "" {
				authorName = sp.AgentID
			}
			post := &platform.Post{
				ID:        uuid.New().String(),
				Platform:  sp.Platform,
				AuthorID:  sp.AgentID,
				AuthorName: authorName,
				Content:   sp.Content,
				Tags:      platform.ExtractTags(sp.Content),
				Timestamp: time.Now(),
			}
			switch sp.Platform {
			case "reddit":
				rdState.AddPost(post)
			default: // "twitter" and any unknown platform default to twitter
				post.Platform = "twitter"
				twState.AddPost(post)
			}
			injected++
		}
		fmt.Printf("Injected %d seed posts from EventConfig\n", injected)
	}

	// ── Main loop ─────────────────────────────────────────────────────────────
	for round := 1; round <= cfg.MaxRounds; round++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Reset per-round action collection for graph memory.
		roundActionsMu.Lock()
		roundActions = roundActions[:0]
		roundActionsMu.Unlock()

		// ── Apply human interventions ─────────────────────────────────────
		if cfg.InterventionQueue != nil {
			events := cfg.InterventionQueue.Drain(round)
			for i := range events {
				e := events[i]
				switch e.Type {
				case "pause":
					// Notify caller that we are pausing.
					if progressCh != nil {
						ev := e
						progressCh <- RoundProgress{
							Round:        round,
							MaxRounds:    cfg.MaxRounds,
							TwitterStat:  twState.GetStats(),
							RedditStat:   rdState.GetStats(),
							Intervention: &ev,
							Paused:       true,
						}
					}
					// Block until a "resume" event arrives or ctx is cancelled.
					if err := waitForResume(ctx, cfg.InterventionQueue); err != nil {
						return err
					}
					// Notify caller that we have resumed.
					if progressCh != nil {
						resumeEv := InterventionEvent{Type: "resume", Round: round}
						progressCh <- RoundProgress{
							Round:        round,
							MaxRounds:    cfg.MaxRounds,
							TwitterStat:  twState.GetStats(),
							RedditStat:   rdState.GetStats(),
							Intervention: &resumeEv,
							Paused:       false,
						}
					}
				case "resume":
					// A stray "resume" with no preceding "pause" is a no-op.
				default:
					ApplyIntervention(twState, e, projectID)
					ApplyIntervention(rdState, e, projectID)
					if progressCh != nil {
						ev := e
						progressCh <- RoundProgress{
							Round:        round,
							MaxRounds:    cfg.MaxRounds,
							TwitterStat:  twState.GetStats(),
							RedditStat:   rdState.GetStats(),
							Intervention: &ev,
						}
					}
				}
			}
		}

		// ── Inject copy for BD reaction (at InjectRound) ─────────────────
		if cfg.CopyReaction != nil {
			injectAt := cfg.CopyReaction.InjectRound
			if injectAt <= 0 {
				injectAt = 1
			}
			if round == injectAt {
				// Inject copy as a Brand post on both platforms
				copyPost := &platform.Post{
					ID:         uuid.New().String(),
					Platform:   "twitter",
					AuthorID:   "brand_agent",
					AuthorName: "Brand",
					Content:    cfg.CopyReaction.CopyText,
					Tags:       platform.ExtractTags(cfg.CopyReaction.CopyText),
					Timestamp:  time.Now(),
				}
				twState.AddPost(copyPost)
				rdCopyPost := &platform.Post{
					ID:         uuid.New().String(),
					Platform:   "reddit",
					AuthorID:   "brand_agent_rd",
					AuthorName: "Brand",
					Content:    cfg.CopyReaction.CopyText,
					Tags:       platform.ExtractTags(cfg.CopyReaction.CopyText),
					Timestamp:  time.Now(),
				}
				rdState.AddPost(rdCopyPost)
			}
		}

		twState.UpdateTrending()
		rdState.UpdateTrending()

		// ── Phase 1: decide (parallel, zero LLM) ─────────────────────────
		// Each agent picks actions based on their timeline. Non-LLM actions
		// (LIKE, REPOST, FOLLOW) are executed immediately. Content-generation
		// actions (CREATE_POST, COMMENT, QUOTE_POST) are collected for batching.
		var roundBatch []contentItem
		var roundBatchMu sync.Mutex

		var wg sync.WaitGroup
		for _, p := range personalities {
			wg.Add(1)
			go func(pers *platform.Personality) {
				defer wg.Done()

				if hasPlatform(cfg.Platforms, "twitter") {
					tl := weightedTimeline(twState, pers.AgentID, influenceByID, 10, round)
					for _, planned := range pers.DecideFromTimeline(tl, cfg.Scenario, round, "twitter") {
						if !planned.NeedLLM {
							if a := execNoLLM(pers, planned, twState, round); a != nil {
								emit(*a)
							}
						} else {
							roundBatchMu.Lock()
							roundBatch = append(roundBatch, contentItem{pers, planned, twState})
							roundBatchMu.Unlock()
						}
					}
				}
				if hasPlatform(cfg.Platforms, "reddit") {
					tl := weightedTimeline(rdState, pers.AgentID+"_rd", influenceByID, 10, round)
					for _, planned := range pers.DecideFromTimeline(tl, cfg.Scenario, round, "reddit") {
						if !planned.NeedLLM {
							if a := execNoLLM(pers, planned, rdState, round); a != nil {
								a.Platform = "reddit"
								emit(*a)
							}
						} else {
							roundBatchMu.Lock()
							roundBatch = append(roundBatch, contentItem{pers, planned, rdState})
							roundBatchMu.Unlock()
						}
					}
				}
			}(p)
		}
		wg.Wait()

		// ── Phase 2: batch content generation ────────────────────────────
		// Default: one LLM call per round for all content (batch JSON).
		// NoLLM: pure templates, zero LLM calls.
		if len(roundBatch) > 0 {
			var contents []string
			var batchErr error

			if cfg.NoLLM {
				contents = make([]string, len(roundBatch))
				for i, it := range roundBatch {
					contents[i] = genContentTemplate(it.pers, it.planned.Topic, it.planned.Type, it.state.Platform, round)
				}
			} else {
				contents, batchErr = ps.batchGenContent(ctx, roundBatch, round)
			}

			// ── Phase 3: create posts + emit ─────────────────────────────
			for i, it := range roundBatch {
				if batchErr != nil {
					emit(platform.Action{
						Round: round, Timestamp: time.Now(),
						Platform:  it.state.Platform,
						AgentID:   it.pers.AgentID,
						AgentName: it.pers.Name,
						Type:      it.planned.Type,
						Success:   false,
						Error:     batchErr.Error(),
					})
					continue
				}

				content := ""
				if i < len(contents) {
					content = contents[i]
				}
				if content == "" {
					content = genContentTemplate(it.pers, it.planned.Topic, it.planned.Type, it.state.Platform, round)
				}

				post := &platform.Post{
					ID:         uuid.New().String(),
					Platform:   it.state.Platform,
					AuthorID:   it.pers.AgentID,
					AuthorName: it.pers.Name,
					Content:    content,
					ParentID:   it.planned.PostID,
					Timestamp:  time.Now(),
					Tags:       platform.ExtractTags(content),
				}
				if it.state.Platform == "reddit" {
					post.Subreddit = pickSub(it.pers.Interests)
				}
				it.state.AddPost(post)

				emit(platform.Action{
					Round:     round,
					Timestamp: time.Now(),
					Platform:  it.state.Platform,
					AgentID:   it.pers.AgentID,
					AgentName: it.pers.Name,
					Type:      it.planned.Type,
					PostID:    post.ID,
					Content:   content,
					Success:   true,
				})
			}
		}

		// ── Flush graph memory edges for this round ───────────────────────
		if memUpdater != nil {
			roundActionsMu.Lock()
			memBatch := append([]platform.Action(nil), roundActions...)
			roundActionsMu.Unlock()
			if err := memUpdater.FlushActions(projectID, memBatch); err != nil {
				if progressCh != nil {
					progressCh <- RoundProgress{
						Round:     round,
						MaxRounds: cfg.MaxRounds,
						Logs:      []string{"graph memory update: " + err.Error()},
					}
				}
			}
		}
	}

	if progressCh != nil {
		progressCh <- RoundProgress{
			Done:        true,
			MaxRounds:   cfg.MaxRounds,
			Round:       cfg.MaxRounds,
			TwitterStat: twState.GetStats(),
			RedditStat:  rdState.GetStats(),
		}
	}
	return nil
}

// weightedTimeline fetches recent posts from state and sorts/samples them
// weighted by the InfluenceWeight of their authors. Higher-influence authors'
// posts are more likely to appear in the agent's visible timeline.
func weightedTimeline(state *platform.State, agentID string, influenceByID map[string]float64, limit int, round int) []*platform.Post {
	// Fetch a larger pool than needed so we can apply weights
	pool := state.RecentPostsExcluding(agentID, limit*4)
	if len(pool) == 0 {
		return nil
	}

	// Build cumulative weight array
	weights := make([]float64, len(pool))
	total := 0.0
	for i, post := range pool {
		// Look up influence weight; strip "_rd" suffix for Reddit user IDs
		authorID := post.AuthorID
		w, ok := influenceByID[authorID]
		if !ok {
			// Try stripping reddit suffix
			stripped := strings.TrimSuffix(authorID, "_rd")
			w, ok = influenceByID[stripped]
			if !ok {
				w = 1.0 // default weight
			}
		}
		if w <= 0 {
			w = 0.01
		}
		total += w
		weights[i] = total
	}

	if total == 0 {
		return pool
	}

	// Weighted sampling without replacement
	rng := rand.New(rand.NewSource(int64(round)*3571 + platform.HashStr(agentID)))
	selected := make([]*platform.Post, 0, limit)
	remaining := append([]*platform.Post(nil), pool...)
	remWeights := append([]float64(nil), weights...)

	for len(selected) < limit && len(remaining) > 0 {
		r := rng.Float64() * remWeights[len(remWeights)-1]
		// Binary search for the chosen index
		lo, hi := 0, len(remWeights)-1
		for lo < hi {
			mid := (lo + hi) / 2
			if remWeights[mid] < r {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		selected = append(selected, remaining[lo])
		// Remove chosen item and recompute cumulative weights
		remaining = append(remaining[:lo], remaining[lo+1:]...)
		remWeights = remWeights[:len(remaining)]
		cum := 0.0
		for j, post := range remaining {
			authorID := post.AuthorID
			w, ok := influenceByID[authorID]
			if !ok {
				stripped := strings.TrimSuffix(authorID, "_rd")
				w, ok = influenceByID[stripped]
				if !ok {
					w = 1.0
				}
			}
			if w <= 0 {
				w = 0.01
			}
			cum += w
			remWeights[j] = cum
		}
	}
	return selected
}

// ─── Action Execution ─────────────────────────────────────────────────────────

// contentItem is a pending content-generation request collected during the decide phase.
type contentItem struct {
	pers    *platform.Personality
	planned platform.PlannedAction
	state   *platform.State
}

// execNoLLM executes all non-content-generating actions.
func execNoLLM(p *platform.Personality, planned platform.PlannedAction, state *platform.State, round int) *platform.Action {
	a := &platform.Action{
		Round:     round,
		Timestamp: time.Now(),
		Platform:  state.Platform,
		AgentID:   p.AgentID,
		AgentName: p.Name,
		Type:      planned.Type,
		PostID:    planned.PostID,
		TargetID:  planned.TargetID,
		Query:     planned.Query,
		Success:   true,
	}
	switch planned.Type {
	case platform.ActLikePost:
		if planned.PostID == "" {
			return nil
		}
		state.LikePost(planned.PostID)
	case platform.ActDislikePost:
		if planned.PostID == "" {
			return nil
		}
		state.DislikePost(planned.PostID)
	case platform.ActLikeComment:
		if planned.PostID == "" {
			return nil
		}
		state.LikeComment(planned.PostID)
	case platform.ActDislikeComment:
		if planned.PostID == "" {
			return nil
		}
		state.DislikeComment(planned.PostID)
	case platform.ActRepost:
		if planned.PostID == "" {
			return nil
		}
		state.Repost(planned.PostID)
	case platform.ActFollow:
		if planned.TargetID == "" {
			return nil
		}
		state.Follow(p.AgentID, planned.TargetID)
		a.TargetID = planned.TargetID
	case platform.ActMute:
		if planned.TargetID == "" {
			return nil
		}
		state.Mute(p.AgentID, planned.TargetID)
		a.TargetID = planned.TargetID
	case platform.ActSearchPosts:
		// Execute search; results feed into agent's next round timeline
		_ = state.SearchPosts(planned.Query, 5)
		a.Query = planned.Query
	case platform.ActSearchUser:
		_ = state.SearchUsers(planned.Query, 3)
		a.Query = planned.Query
	case platform.ActTrend:
		// Agent views trending — no state mutation
	case platform.ActRefresh:
		// Agent refreshes feed — no state mutation
	case platform.ActDoNothing:
		// Explicit no-op
	default:
		return nil
	}
	return a
}

// genContent calls the LLM to produce post/comment text.
// stance and sentimentBias adjust the tone instruction in the prompt.
func (ps *PlatformSim) genContent(
	ctx context.Context,
	p *platform.Personality,
	topic, actionType, plat string,
	stance string,
	sentimentBias float64,
) (string, error) {
	style := "1-2 sentences"
	if actionType == platform.ActCreatePost && p.Verbosity > 0.6 {
		style = "2-3 sentences"
	}

	// Determine tone based on stance and sentimentBias
	tone := ""
	switch stance {
	case "opposing":
		tone = "use critical/negative tone"
	case "supportive":
		tone = "use positive/supportive tone"
	case "observer":
		tone = "use neutral observational tone"
	default: // "neutral" and anything else
		if sentimentBias > 0.3 {
			tone = "use positive tone"
		} else if sentimentBias < -0.3 {
			tone = "use critical tone"
		} else if p.Positivity > 0.6 {
			tone = "use positive tone"
		} else if p.Positivity < 0.3 {
			tone = "use critical tone"
		}
	}

	prompt := fmt.Sprintf("Name:%s Type:%s Platform:%s Style:%s %s\nTopic:%s",
		p.Name, p.NodeType, plat, style, tone, topic)

	const sys = "Generate authentic social media content. Output ONLY the post text, no quotes or labels."
	return ps.llm.System(ctx, sys, prompt)
}

// buildPersonalities enriches all nodes with LLM-generated personality traits.
// Falls back to FromNode() defaults on error.
// If simCfg is non-nil, applies it after building personalities.
func (ps *PlatformSim) buildPersonalities(
	ctx context.Context,
	nodes []db.Node,
	scenario string,
	concurrency int,
	simCfg *platform.SimConfig,
) []*platform.Personality {
	results := make([]*platform.Personality, len(nodes))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, node := range nodes {
		wg.Add(1)
		go func(idx int, n db.Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			base := platform.FromNode(n, idx)

			var enriched struct {
				Interests   []string `json:"interests"`
				Style       string   `json:"style"`
				Activity    float64  `json:"activity"`
				Positivity  float64  `json:"positivity"`
				Leadership  float64  `json:"leadership"`
				Originality float64  `json:"originality"`
			}
			// Compressed prompt: name|type|scenario → behavioral traits
			prompt := fmt.Sprintf("%s|%s|scenario: %s", n.Name, n.Type, truncScenario(scenario))
			err := ps.llm.JSON(ctx,
				`Given "name|type|scenario", infer social media behavior. Return JSON only:
{"interests":["topic"],"style":"informative|emotional|analytical|humorous","activity":0.5,"positivity":0.5,"leadership":0.5,"originality":0.4}`,
				prompt, &enriched)

			if err == nil && len(enriched.Interests) > 0 {
				base.Interests = enriched.Interests
				base.PostStyle = enriched.Style
				base.ActivityLevel = clamp01(enriched.Activity)
				base.Positivity = clamp01(enriched.Positivity)
				base.Leadership = clamp01(enriched.Leadership)
				base.Originality = clamp01(enriched.Originality)
			}
			results[idx] = base
		}(i, node)
	}
	wg.Wait()

	// Apply SimConfig if provided
	if simCfg != nil {
		platform.ApplySimConfig(results, simCfg)
	}

	return results
}

// ─── Pause / Resume ───────────────────────────────────────────────────────────

// waitForResume polls the InterventionQueue until a "resume" event is found or
// ctx is cancelled. This implements the blocking-pause behaviour that mirrors
// MiroFish's simulation_ipc.py pause/resume signal flow.
func waitForResume(ctx context.Context, q *InterventionQueue) error {
	const pollInterval = 200 * time.Millisecond
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Peek at all pending events; consume a "resume" if present.
			if q.drainResume() {
				return nil
			}
		}
	}
}

// drainResume removes the first "resume" event from the queue and returns true.
// Returns false if no "resume" event is found.
func (q *InterventionQueue) drainResume() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, e := range q.events {
		if e.Type == "resume" {
			q.events = append(q.events[:i], q.events[i+1:]...)
			return true
		}
	}
	return false
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func hasPlatform(platforms []string, p string) bool {
	if len(platforms) == 0 {
		return true // default: all
	}
	for _, pl := range platforms {
		if pl == p {
			return true
		}
	}
	return false
}

func pickSub(interests []string) string {
	if len(interests) == 0 {
		return "general"
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	sub := interests[rng.Intn(len(interests))]
	return strings.ToLower(strings.ReplaceAll(sub, " ", ""))
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func truncScenario(s string) string {
	runes := []rune(s)
	if len(runes) <= 2000 {
		return s
	}
	return string(runes[:2000]) + "…"
}

// ─── Low-token helpers ────────────────────────────────────────────────────────

// buildPersonalitiesLow builds personalities from node defaults — no LLM calls.
func (ps *PlatformSim) buildPersonalitiesLow(nodes []db.Node, simCfg *platform.SimConfig) []*platform.Personality {
	results := make([]*platform.Personality, len(nodes))
	for i, n := range nodes {
		results[i] = platform.FromNode(n, i)
	}
	if simCfg != nil {
		platform.ApplySimConfig(results, simCfg)
	}
	return results
}

// genContentTemplate generates post content using local templates — no LLM calls.
// Results vary by stance, action type, and round for diversity.
func genContentTemplate(p *platform.Personality, topic, actionType, plat string, round int) string {
	rng := rand.New(rand.NewSource(int64(round)*1337 + platform.HashStr(p.AgentID+actionType)))
	short := clip(topic, 60)

	type tmpl struct{ supportive, opposing, observer, neutral string }

	var pool []tmpl
	switch actionType {
	case platform.ActCreatePost:
		pool = []tmpl{
			{
				supportive: "Thinking a lot about " + short + " lately — and I genuinely believe this matters.",
				opposing:   "Hot take: the discourse around " + short + " is missing the real point entirely.",
				observer:   "Interesting how " + short + " keeps coming up. Worth paying attention to.",
				neutral:    "Just saw another piece on " + short + ". Lots to unpack here.",
			},
			{
				supportive: "We need more conversations about " + short + ". This is important.",
				opposing:   "I'll be honest — I'm skeptical about " + short + ". Not convinced yet.",
				observer:   "Following the " + short + " discussion closely. Complex issue.",
				neutral:    short + " is trending again. Here are my quick thoughts.",
			},
			{
				supportive: short + " — this is exactly the kind of thing we should be pushing for.",
				opposing:   "People are getting too worked up about " + short + ". Let's be realistic.",
				observer:   "Watching the " + short + " debate unfold in real time. Fascinating.",
				neutral:    "My take on " + short + ": it's nuanced and deserves more careful thought.",
			},
		}
	case platform.ActCreateComment:
		pool = []tmpl{
			{
				supportive: "Totally agree with this perspective on " + short + ".",
				opposing:   "Respectfully disagree. " + short + " isn't as straightforward as implied.",
				observer:   "This is a fair point about " + short + ". Both sides have merit.",
				neutral:    "Interesting angle on " + short + ". I see where you're coming from.",
			},
			{
				supportive: "Yes! This is exactly what I've been saying about " + short + ".",
				opposing:   "I'd push back on this framing of " + short + " — it oversimplifies things.",
				observer:   "Good thread. " + short + " is worth more nuanced discussion.",
				neutral:    "Fair take. " + short + " is definitely something people feel strongly about.",
			},
		}
	default: // QUOTE_POST
		pool = []tmpl{
			{
				supportive: "Sharing this because " + short + " deserves wider attention.",
				opposing:   "Quoting this to add some context — " + short + " is more complicated.",
				observer:   "Worth reading for anyone following " + short + ".",
				neutral:    "Relevant to the ongoing " + short + " conversation.",
			},
		}
	}

	t := pool[rng.Intn(len(pool))]
	switch p.Stance {
	case "supportive":
		return t.supportive
	case "opposing":
		return t.opposing
	case "observer":
		return t.observer
	default:
		return t.neutral
	}
}
