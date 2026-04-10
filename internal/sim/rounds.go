package sim

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"fishnet/internal/db"
	"fishnet/internal/llm"
	"fishnet/internal/platform"
)

// ─── Config & Progress ────────────────────────────────────────────────────────

// Simulation mode constants.
const (
	// ModeNoLLM — zero LLM calls per round; decisions are math-based, content
	// is generated from local templates. Near-zero token cost.
	ModeNoLLM = "nollm"

	// ModeBatch — 1 LLM call per round (batched) for content generation;
	// decisions are still math-based. Default mode.
	ModeBatch = "batch"

	// ModeHeavy — 1 LLM call per agent per round; LLM decides WHAT to do AND
	// generates content in one call. Produces the most authentic, stance-aware
	// behaviour at the cost of N*rounds total calls.
	ModeHeavy = "heavy"
)

// RoundConfig configures a full multi-round social simulation.
type RoundConfig struct {
	Scenario    string
	MaxRounds   int
	MaxAgents   int
	Platforms   []string // "twitter" | "reddit"
	OutputDir   string   // dir for actions.jsonl; empty = skip
	Concurrency int      // max concurrent LLM calls (default 6)
	SimConfig   *platform.SimConfig // optional; applied after buildPersonalities

	// Mode selects the simulation fidelity level:
	//   ModeNoLLM ("nollm") — template content, math decisions, zero LLM
	//   ModeBatch  ("batch") — 1 batch LLM call/round for content  [default]
	//   ModeHeavy  ("heavy") — 1 LLM call/agent/round for decisions + content
	// The legacy NoLLM bool is still respected (maps to ModeNoLLM).
	Mode string

	// FeedWeights overrides the 4-factor ranking weights used to build each
	// agent's timeline. nil → platform.DefaultFeedWeights.
	FeedWeights *platform.FeedWeights

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

	// NoLLM disables ALL LLM calls (legacy flag; sets Mode = ModeNoLLM when Mode is "").
	NoLLM bool

	// Simulation clock settings.
	// SimStartTime is the wall-clock moment that corresponds to round 1.
	// Defaults to time.Now() at Run() entry if zero.
	SimStartTime time.Time

	// MinutesPerRound is how many simulated minutes each round represents.
	// Defaults to 60 (1 hour per round) if zero.
	// Example: 60 → each round = 1 hour; 10 → each round = 10 minutes.
	MinutesPerRound int
}

// heavyAgentTimeout is the per-agent deadline for a single heavyDecide call.
// Agents that exceed this are treated as DO_NOTHING so the round can proceed.
const heavyAgentTimeout = 45 * time.Second

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
	// Heartbeat fields — set when no Action is present, for UI liveness feedback.
	Heartbeat   bool
	AgentsDone  int
	AgentsTotal int
	// SimTime is the simulated wall-clock time for this round.
	// Round 1 = SimStartTime, round N = SimStartTime + (N-1)*MinutesPerRound minutes.
	SimTime time.Time
}

// ─── Platform Simulation ──────────────────────────────────────────────────────

// PlatformSim runs a multi-round Twitter + Reddit simulation.
//
// Efficiency model (by mode):
//
//	ModeNoLLM — zero LLM calls; math decisions + template content
//	ModeBatch  — 1 batch LLM call/round for content; math decisions  [default]
//	ModeHeavy  — 1 LLM call/agent/round; LLM decides + generates content
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
	if cfg.SimStartTime.IsZero() {
		cfg.SimStartTime = time.Now()
	}
	if cfg.MinutesPerRound <= 0 {
		cfg.MinutesPerRound = 60 // default: 1 hour per round
	}

	// ── Resolve mode ─────────────────────────────────────────────────────────
	if cfg.Mode == "" {
		if cfg.NoLLM {
			cfg.Mode = ModeNoLLM
		} else {
			cfg.Mode = ModeBatch
		}
	}

	// ── Resolve feed weights ─────────────────────────────────────────────────
	feedWeights := platform.DefaultFeedWeights
	if cfg.FeedWeights != nil {
		feedWeights = *cfg.FeedWeights
	}

	// ── Open log file early (needed before personality building) ─────────────
	logf := func(format string, args ...interface{}) {} // no-op when no OutputDir
	var logFile *os.File
	if cfg.OutputDir != "" {
		if err2 := os.MkdirAll(cfg.OutputDir, 0755); err2 == nil {
			logFile, _ = os.OpenFile(filepath.Join(cfg.OutputDir, "sim.log"),
				os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if logFile != nil {
				defer logFile.Close()
				logf = func(format string, args ...interface{}) {
					fmt.Fprintf(logFile, "[%s] "+format+"\n",
						append([]interface{}{time.Now().Format("15:04:05.000")}, args...)...)
				}
			}
		}
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
		logf("PERSONALITIES pre-built n=%d", len(cfg.Personalities))
		personalities = cfg.Personalities
	} else if cfg.Mode == ModeNoLLM {
		logf("PERSONALITIES building low-cost n=%d", len(nodes))
		personalities = ps.buildPersonalitiesLow(nodes, cfg.SimConfig)
		logf("PERSONALITIES done n=%d", len(personalities))
	} else {
		logf("PERSONALITIES building via LLM n=%d concurrency=%d", len(nodes), cfg.Concurrency)
		t0 := time.Now()
		personalities = ps.buildPersonalities(ctx, nodes, cfg.Scenario, cfg.Concurrency, cfg.SimConfig)
		logf("PERSONALITIES done n=%d elapsed=%s", len(personalities), time.Since(t0).Round(time.Second))
	}

	// ── Build persona fingerprints for token compression (skip in NoLLM mode) ──
	if cfg.Mode != ModeNoLLM {
		logf("FINGERPRINTS building n=%d", len(personalities))
		t0 := time.Now()
		ps.buildFingerprints(ctx, personalities, cfg.Concurrency)
		logf("FINGERPRINTS done elapsed=%s", time.Since(t0).Round(time.Second))
	}

	// ── Build InfluenceWeight and CommunityID lookup maps ────────────────────
	influenceByID := make(map[string]float64, len(personalities))
	communityByID := make(map[string]int, len(personalities))
	for _, p := range personalities {
		if p != nil {
			influenceByID[p.AgentID] = p.InfluenceWeight
			communityByID[p.AgentID] = p.CommunityID
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

	// ── Open actions.jsonl ────────────────────────────────────────────────────
	var outFile *os.File
	if cfg.OutputDir != "" {
		if err := os.MkdirAll(cfg.OutputDir, 0755); err == nil {
			outFile, _ = os.Create(filepath.Join(cfg.OutputDir, "actions.jsonl"))
			if outFile != nil {
				defer outFile.Close()
			}
		}
	}

	logf("START scenario=%q mode=%s rounds=%d agents=%d platforms=%v",
		cfg.Scenario, cfg.Mode, cfg.MaxRounds, len(personalities), cfg.Platforms)

	// roundActions accumulates actions within a single round for graph memory.
	var roundActions []platform.Action
	var roundActionsMu sync.Mutex

	// currentSimTime is updated at the start of each round and captured by emit.
	var currentSimTime time.Time

	emit := func(a platform.Action) {
		if outFile != nil {
			outFile.Write(a.MarshalLine())
		}
		if progressCh != nil {
			var logs []string
			if !a.Success && a.Error != "" {
				logs = []string{fmt.Sprintf("[%s/%s] %s: %s", a.Platform, a.AgentName, a.Type, a.Error)}
			}
			// Non-blocking send: heavy mode spawns many concurrent goroutines that
			// all call emit(); a blocking send causes all semaphore slots to fill up
			// and the simulation deadlocks. The file still captures every action.
			select {
			case progressCh <- RoundProgress{
				Round:       a.Round,
				MaxRounds:   cfg.MaxRounds,
				Action:      a,
				TwitterStat: twState.GetStats(),
				RedditStat:  rdState.GetStats(),
				Logs:        logs,
				SimTime:     currentSimTime,
			}:
			default: // channel full — drop UI update, sim continues
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
			logf("CANCELLED round=%d", round)
			return ctx.Err()
		default:
		}
		// Simulation clock: round 1 = SimStartTime, each subsequent round advances by MinutesPerRound.
		simTime := cfg.SimStartTime.Add(time.Duration(round-1) * time.Duration(cfg.MinutesPerRound) * time.Minute)
		currentSimTime = simTime
		logf("ROUND_START round=%d/%d simTime=%s", round, cfg.MaxRounds, simTime.Format("2006-01-02 15:04 MST"))

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

		if cfg.Mode == ModeHeavy {
			// ════════════════════════════════════════════════════════════════
			// Heavy mode: 1 LLM call per agent — decides + generates content.
			// ════════════════════════════════════════════════════════════════
			sem := make(chan struct{}, cfg.Concurrency)
			var wg sync.WaitGroup
			var agentsDone atomic.Int32
			agentsTotal := int32(len(personalities))

			// Heartbeat goroutine: emits a liveness ping every 2s so the TUI
			// doesn't appear frozen while agents are waiting for LLM responses.
			hbDone := make(chan struct{})
			go func() {
				ticker := time.NewTicker(2 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						if progressCh != nil {
							done := int(agentsDone.Load())
							select {
							case progressCh <- RoundProgress{
								Round:       round,
								MaxRounds:   cfg.MaxRounds,
								TwitterStat: twState.GetStats(),
								RedditStat:  rdState.GetStats(),
								Heartbeat:   true,
								AgentsDone:  done,
								AgentsTotal: int(agentsTotal),
								SimTime:     currentSimTime,
							}:
							default:
							}
						}
					case <-hbDone:
						return
					}
				}
			}()

			for _, p := range personalities {
				wg.Add(1)
				go func(pers *platform.Personality) {
					defer wg.Done()
					defer agentsDone.Add(1)
					sem <- struct{}{}
					defer func() { <-sem }()

					runHeavyPlatform := func(state *platform.State, platName string) {
						tl := platform.RankedFeed(state, pers, 10, influenceByID, feedWeights, round, communityByID)
						// Mark these posts as seen so they don't reappear in future rounds
						seenIDs := make([]string, len(tl))
						for i, post := range tl { seenIDs[i] = post.ID }
						state.MarkSeen(pers.AgentID, seenIDs)
						// Per-agent timeout: a stuck LLM call should not block the whole round.
						agentCtx, agentCancel := context.WithTimeout(ctx, heavyAgentTimeout)
						defer agentCancel()
						t0 := time.Now()
						logf("AGENT_START r=%d agent=%q plat=%s feed=%d", round, pers.Name, platName, len(tl))
						results := ps.heavyDecide(agentCtx, pers, tl, cfg.Scenario, round, platName)
						elapsed := time.Since(t0).Round(time.Millisecond)
						if agentCtx.Err() != nil {
							logf("AGENT_TIMEOUT r=%d agent=%q plat=%s after=%s", round, pers.Name, platName, elapsed)
						} else {
							logf("AGENT_DONE r=%d agent=%q plat=%s elapsed=%s actions=%d", round, pers.Name, platName, elapsed, len(results))
						}
						for _, res := range results {
							planned := res.Action
							content := res.Content
							logParts := []string{}
							if res.Reason != "" {
								logParts = append(logParts, "["+pers.Name+"] "+res.Reason)
							}

							switch planned.Type {
							case platform.ActCreatePost, platform.ActQuotePost, platform.ActCreateComment:
								if content == "" {
									content = genContentTemplate(pers, planned.Topic, planned.Type, platName, round)
								}
								post := &platform.Post{
									ID:         uuid.New().String(),
									Platform:   platName,
									AuthorID:   pers.AgentID,
									AuthorName: pers.Name,
									Content:    content,
									ParentID:   planned.PostID,
									Timestamp:  time.Now(),
									Tags:       platform.PostTagsOrFallback(content, pers.Interests),
								}
								if platName == "reddit" {
									post.Subreddit = pickSub(pers.Interests)
								}
								state.AddPost(post)
								if planned.PostID != "" {
									// Quote/comment on someone else's post → record interaction
									if authorID := state.PostAuthorID(planned.PostID); authorID != "" {
										state.RecordInteraction(pers.AgentID, authorID)
									}
								}
								emit(platform.Action{
									Round: round, Timestamp: time.Now(),
									Platform:  platName,
									AgentID:   pers.AgentID, AgentName: pers.Name,
									Type: planned.Type, PostID: post.ID,
									Content: content, Success: true,
									Error: strings.Join(logParts, "; "),
								})

							default:
								if a := execNoLLM(pers, planned, state, round); a != nil {
									// Record interactions for likes/reposts
									if planned.PostID != "" {
										switch planned.Type {
										case platform.ActLikePost, platform.ActRepost,
											platform.ActDislikePost, platform.ActLikeComment:
											if authorID := state.PostAuthorID(planned.PostID); authorID != "" {
												state.RecordInteraction(pers.AgentID, authorID)
											}
										}
									}
									// Drive interest drift from liked/reposted content
									if planned.PostID != "" {
										switch planned.Type {
										case platform.ActLikePost, platform.ActRepost:
											tags := state.PostTags(planned.PostID)
											if len(tags) > 0 {
												state.RecordLikedTags(pers.AgentID, tags)
											}
										}
									}
									if platName == "reddit" {
										a.Platform = "reddit"
									}
									if len(logParts) > 0 {
										a.Error = strings.Join(logParts, "; ")
									}
									emit(*a)
								}
							}
						}
					}

					if hasPlatform(cfg.Platforms, "twitter") {
						runHeavyPlatform(twState, "twitter")
					}
					if hasPlatform(cfg.Platforms, "reddit") {
						runHeavyPlatform(rdState, "reddit")
					}
				}(p)
			}
			logf("HEAVY_WAIT r=%d waiting for %d agents", round, len(personalities))
			wg.Wait()
			close(hbDone) // stop heartbeat goroutine
			logf("ROUND_END r=%d mode=heavy events_tw=%d events_rd=%d", round, twState.GetStats().Posts, rdState.GetStats().Posts)
		} else {
			// ════════════════════════════════════════════════════════════════
			// Batch / NoLLM mode
			// Phase 1: math-based decide (parallel, zero LLM).
			//   Non-content actions execute immediately.
			//   Content actions collected for batch generation.
			// ════════════════════════════════════════════════════════════════
			var roundBatch []contentItem
			var roundBatchMu sync.Mutex

			var wg sync.WaitGroup
			for _, p := range personalities {
				wg.Add(1)
				go func(pers *platform.Personality) {
					defer wg.Done()

					runBatchPlatform := func(state *platform.State, platName string) {
						tl := platform.RankedFeed(state, pers, 10, influenceByID, feedWeights, round, communityByID)
						// Mark these posts as seen so they don't reappear in future rounds
						seenIDs := make([]string, len(tl))
						for i, post := range tl { seenIDs[i] = post.ID }
						state.MarkSeen(pers.AgentID, seenIDs)
						for _, planned := range pers.DecideAt(tl, cfg.Scenario, round, simTime, platName) {
							if !planned.NeedLLM {
								if a := execNoLLM(pers, planned, state, round); a != nil {
									// Record interactions for likes/reposts
									if planned.PostID != "" {
										switch planned.Type {
										case platform.ActLikePost, platform.ActRepost,
											platform.ActDislikePost, platform.ActLikeComment:
											if authorID := state.PostAuthorID(planned.PostID); authorID != "" {
												state.RecordInteraction(pers.AgentID, authorID)
											}
										}
									}
									// Drive interest drift from liked/reposted content
									if planned.PostID != "" {
										switch planned.Type {
										case platform.ActLikePost, platform.ActRepost:
											tags := state.PostTags(planned.PostID)
											if len(tags) > 0 {
												state.RecordLikedTags(pers.AgentID, tags)
											}
										}
									}
									if platName == "reddit" {
										a.Platform = "reddit"
									}
									emit(*a)
								}
							} else {
								roundBatchMu.Lock()
								roundBatch = append(roundBatch, contentItem{pers, planned, state})
								roundBatchMu.Unlock()
							}
						}
					}

					if hasPlatform(cfg.Platforms, "twitter") {
						runBatchPlatform(twState, "twitter")
					}
					if hasPlatform(cfg.Platforms, "reddit") {
						runBatchPlatform(rdState, "reddit")
					}
				}(p)
			}
			wg.Wait()

			// ── Phase 2: batch content generation ────────────────────────────
			if len(roundBatch) > 0 {
				var contents []string
				var batchErr error

				if cfg.Mode == ModeNoLLM {
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
							AgentID:   it.pers.AgentID, AgentName: it.pers.Name,
							Type:    it.planned.Type,
							Success: false, Error: batchErr.Error(),
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
						Tags:       platform.PostTagsOrFallback(content, it.pers.Interests),
					}
					if it.state.Platform == "reddit" {
						post.Subreddit = pickSub(it.pers.Interests)
					}
					it.state.AddPost(post)
					// Record interaction for quote/comment (replying to someone)
					if it.planned.PostID != "" {
						if authorID := it.state.PostAuthorID(it.planned.PostID); authorID != "" {
							it.state.RecordInteraction(it.pers.AgentID, authorID)
						}
					}

					emit(platform.Action{
						Round:     round,
						Timestamp: time.Now(),
						Platform:  it.state.Platform,
						AgentID:   it.pers.AgentID, AgentName: it.pers.Name,
						Type:    it.planned.Type,
						PostID:  post.ID, Content: content,
						Success: true,
					})
				}
			}
		} // end batch/nollm branch

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
			SimTime:     currentSimTime,
		}
	}
	return nil
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

// buildFingerprints generates a compressed persona fingerprint for each personality
// using concurrent LLM calls. Falls back to a template if LLM fails.
// Fingerprints are cached in p.Fingerprint and reused every round.
func (ps *PlatformSim) buildFingerprints(ctx context.Context, personalities []*platform.Personality, concurrency int) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, p := range personalities {
		if p == nil || p.Fingerprint != "" {
			continue
		}
		wg.Add(1)
		go func(pers *platform.Personality) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			prompt := fmt.Sprintf("%s|%s|%s|stance:%s|style:%s|interests:%s",
				pers.Name, pers.NodeType, truncScenario(pers.Bio),
				pers.Stance, pers.PostStyle,
				strings.Join(pers.Interests, ","))

			const sys = `Compress this agent persona into a single line ≤60 words for a social media simulation prompt.
Include: name, role, stance, posting style, top 2-3 interests. Be specific and terse.
Output ONLY the compressed line, no JSON, no labels.`

			result, err := ps.llm.System(ctx, sys, prompt)
			if err != nil || result == "" {
				// Fallback: template fingerprint
				interests := strings.Join(pers.Interests, ", ")
				if len([]rune(interests)) > 40 {
					interests = string([]rune(interests)[:40]) + "…"
				}
				pers.Fingerprint = fmt.Sprintf("%s | %s | stance:%s | style:%s | interests:%s",
					pers.Name, pers.NodeType, pers.Stance, pers.PostStyle, interests)
			} else {
				pers.Fingerprint = strings.TrimSpace(result)
			}
		}(p)
	}
	wg.Wait()
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
