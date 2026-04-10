package sim

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"fishnet/internal/platform"
)

// ─── Branch Types ─────────────────────────────────────────────────────────────

// Branch represents a simulation fork point.
// When a branch condition triggers, the simulation creates a parallel timeline.
type Branch struct {
	ID          string // "branch-001"
	Name        string // human-readable label
	TriggerAt   int    // round to branch at (0 = auto-detect key moments; currently unused — all branches start from round 0)
	Scenario    string // modified scenario for this branch (e.g. "add: government intervention")
	Description string // what changed
}

// BranchResult captures outcome of one branch timeline.
type BranchResult struct {
	Branch      Branch
	Actions     []platform.Action
	TwitterStat platform.Stats
	RedditStat  platform.Stats
	Summary     string // LLM-generated 2-sentence outcome summary
}

// MultiBranchConfig configures a branching simulation run.
type MultiBranchConfig struct {
	Base        RoundConfig
	Branches    []Branch
	MaxBranches int // max simultaneous branches (default 3)
}

// ─── RunMultiBranch ───────────────────────────────────────────────────────────

// RunMultiBranch runs the base scenario + all branches concurrently.
// Each branch starts fresh from round 0 with a modified scenario.
// Returns one BranchResult per branch (base first, then branches sorted by name).
func (ps *PlatformSim) RunMultiBranch(
	ctx context.Context,
	projectID string,
	cfg MultiBranchConfig,
	progressCh chan<- RoundProgress,
) ([]BranchResult, error) {
	maxB := cfg.MaxBranches
	if maxB <= 0 {
		maxB = 3
	}

	// Build the full list: base (no branch) + each branch, capped at maxB+1
	type branchRun struct {
		isBase bool
		branch Branch
	}

	runs := make([]branchRun, 0, 1+len(cfg.Branches))
	runs = append(runs, branchRun{isBase: true})
	for i, b := range cfg.Branches {
		if i >= maxB {
			break
		}
		runs = append(runs, branchRun{branch: b})
	}

	results := make([]BranchResult, len(runs))
	var wg sync.WaitGroup

	for i, run := range runs {
		wg.Add(1)
		go func(idx int, br branchRun) {
			defer wg.Done()

			// Build per-branch scenario
			scenario := cfg.Base.Scenario
			branchObj := br.branch
			if !br.isBase {
				scenario = cfg.Base.Scenario + "\n\nVariant: " + br.branch.Description
				if branchObj.ID == "" {
					branchObj.ID = fmt.Sprintf("branch-%03d", idx)
				}
			} else {
				branchObj = Branch{ID: "base", Name: "Base", Description: "original scenario"}
			}

			// Clone base config with modified scenario
			roundCfg := cfg.Base
			roundCfg.Scenario = scenario

			// Collect actions for this branch
			var mu sync.Mutex
			var actions []platform.Action
			var twStat, rdStat platform.Stats

			// We need our own progress channel so we don't mix output
			branchCh := make(chan RoundProgress, 128)

			done := make(chan error, 1)
			go func() {
				done <- ps.Run(ctx, projectID, roundCfg, branchCh)
			}()

			for prog := range branchCh {
				if prog.Done {
					mu.Lock()
					twStat = prog.TwitterStat
					rdStat = prog.RedditStat
					mu.Unlock()
					break
				}
				mu.Lock()
				if prog.Action.AgentID != "" {
					actions = append(actions, prog.Action)
				}
				mu.Unlock()
				// Forward to caller's channel if present (tagged by branch)
				if progressCh != nil {
					progressCh <- prog
				}
			}

			if err := <-done; err != nil {
				results[idx] = BranchResult{Branch: branchObj}
				return
			}

			// Generate LLM summary comparing stats
			summary := ps.branchSummary(ctx, branchObj, actions, twStat, rdStat)

			results[idx] = BranchResult{
				Branch:      branchObj,
				Actions:     actions,
				TwitterStat: twStat,
				RedditStat:  rdStat,
				Summary:     summary,
			}
		}(i, run)
	}

	wg.Wait()

	// Sort: base first, then branches by Name
	base := results[0]
	branches := results[1:]
	sort.Slice(branches, func(i, j int) bool {
		return branches[i].Branch.Name < branches[j].Branch.Name
	})
	ordered := append([]BranchResult{base}, branches...)
	return ordered, nil
}

// branchSummary generates a 2-sentence LLM summary for a branch outcome.
func (ps *PlatformSim) branchSummary(
	ctx context.Context,
	b Branch,
	actions []platform.Action,
	twStat, rdStat platform.Stats,
) string {
	posts := twStat.Posts + rdStat.Posts
	totalActions := len(actions)

	// Count sentiment from action types as a rough proxy
	positiveCount := 0
	for _, a := range actions {
		if a.Type == platform.ActLikePost || a.Type == platform.ActRepost || a.Type == platform.ActLikeComment {
			positiveCount++
		}
	}
	sentiment := "neutral"
	if totalActions > 0 {
		ratio := float64(positiveCount) / float64(totalActions)
		if ratio > 0.5 {
			sentiment = "positive"
		} else if ratio < 0.2 {
			sentiment = "negative"
		}
	}

	prompt := fmt.Sprintf(
		"Branch '%s' (variant: %s). Stats: %d total posts (tw:%d rd:%d), %d actions, overall sentiment: %s. "+
			"Summarize this simulation outcome in exactly 2 sentences.",
		b.Name, b.Description, posts, twStat.Posts, rdStat.Posts, totalActions, sentiment,
	)
	summary, _ := ps.llm.System(ctx,
		"You are a simulation analyst. Be concise and insightful.",
		prompt,
	)
	if summary == "" {
		summary = fmt.Sprintf("%d posts, %d actions, sentiment: %s.", posts, totalActions, sentiment)
	}
	return summary
}

// ─── AutoBranches ─────────────────────────────────────────────────────────────

// AutoBranches generates branch suggestions using LLM given a scenario.
// Returns 2-3 meaningful "what if" variants.
func (ps *PlatformSim) AutoBranches(ctx context.Context, scenario string) ([]Branch, error) {
	prompt := fmt.Sprintf(
		"Given scenario '%s', suggest 2-3 'what if' variants as JSON: "+
			`[{"name":"...","description":"..."}]`+
			" Each description must be under 80 characters. Return only valid JSON array.",
		truncScenario(scenario),
	)

	var raw []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	err := ps.llm.JSON(ctx,
		"You are a scenario planner. Generate distinct, plausible 'what if' variants for a social simulation.",
		prompt,
		&raw,
	)
	if err != nil {
		return nil, fmt.Errorf("auto-branches LLM: %w", err)
	}

	branches := make([]Branch, 0, len(raw))
	for i, r := range raw {
		desc := r.Description
		runes := []rune(desc)
		if len(runes) > 80 {
			desc = string(runes[:80])
		}
		branches = append(branches, Branch{
			ID:          fmt.Sprintf("branch-%03d", i+1),
			Name:        r.Name,
			Description: desc,
		})
	}
	return branches, nil
}

// ─── FormatBranchResults ──────────────────────────────────────────────────────

// FormatBranchResults renders multi-branch results in the CLI output format.
func FormatBranchResults(results []BranchResult) string {
	var sb fmt.Stringer
	_ = sb
	out := ""
	for i, r := range results {
		label := r.Branch.Name
		if i == 0 {
			label = "Base"
		}
		posts := r.TwitterStat.Posts + r.RedditStat.Posts
		actions := len(r.Actions)
		out += fmt.Sprintf("%-20s %d posts, %d actions\n", label+":", posts, actions)
		if r.Summary != "" {
			out += fmt.Sprintf("  %s\n", r.Summary)
		}
	}
	return out
}
