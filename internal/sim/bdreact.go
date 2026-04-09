package sim

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"fishnet/internal/platform"
)

// ─── CopyReaction Types ───────────────────────────────────────────────────────

// CopyReactionConfig configures BD-style copy testing in the simulation.
type CopyReactionConfig struct {
	CopyText    string   // the copy/content to test
	CopyTitle   string   // optional headline
	Platform    string   // "twitter" | "reddit"
	InjectRound int      // which round to inject (default: 1)
	ReactorIDs  []string // which agent IDs should react (empty = all active agents)
}

// CopyReactionResult captures per-agent reaction.
type CopyReactionResult struct {
	AgentID   string
	AgentName string
	Reaction  string // "share" | "comment" | "like" | "ignore" | "oppose"
	Response  string // generated response text
	Sentiment string // "positive" | "neutral" | "negative"
	Score     int    // 1-10
}

// ─── SimulateCopyReactions ────────────────────────────────────────────────────

// SimulateCopyReactions runs a focused simulation of how agents react to a piece of copy.
// Used for BD/marketing: test messaging before launch.
// sem is an optional external semaphore (may be nil; a default one will be created).
func (ps *PlatformSim) SimulateCopyReactions(
	ctx context.Context,
	projectID string,
	copyConfig CopyReactionConfig,
	agents []*platform.Personality,
	sem chan struct{},
) ([]CopyReactionResult, error) {
	if len(agents) == 0 {
		return nil, fmt.Errorf("no agents provided for copy reaction simulation")
	}

	// Build reactor set
	reactorSet := make(map[string]bool, len(copyConfig.ReactorIDs))
	for _, id := range copyConfig.ReactorIDs {
		reactorSet[id] = true
	}
	useAll := len(reactorSet) == 0

	// Create default semaphore if none provided
	if sem == nil {
		sem = make(chan struct{}, 6)
	}

	// Build copy prompt text
	copyText := copyConfig.CopyText
	if copyConfig.CopyTitle != "" {
		copyText = copyConfig.CopyTitle + "\n\n" + copyConfig.CopyText
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	results := make([]CopyReactionResult, 0, len(agents))

	for _, agent := range agents {
		if agent == nil {
			continue
		}
		if !useAll && !reactorSet[agent.AgentID] {
			continue
		}

		wg.Add(1)
		go func(p *platform.Personality) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			result, err := ps.askCopyReaction(ctx, p, copyText)
			if err != nil {
				// On error, record an ignore with neutral sentiment
				mu.Lock()
				results = append(results, CopyReactionResult{
					AgentID:   p.AgentID,
					AgentName: p.Name,
					Reaction:  "ignore",
					Response:  "",
					Sentiment: "neutral",
					Score:     1,
				})
				mu.Unlock()
				return
			}
			result.AgentID = p.AgentID
			result.AgentName = p.Name

			mu.Lock()
			results = append(results, result)
			mu.Unlock()
		}(agent)
	}
	wg.Wait()

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results, nil
}

// askCopyReaction asks a single agent personality to react to copy content.
func (ps *PlatformSim) askCopyReaction(
	ctx context.Context,
	p *platform.Personality,
	copyText string,
) (CopyReactionResult, error) {
	system := fmt.Sprintf(
		"You are %s, a %s with style: %s. Stance: %s. React authentically.",
		p.Name, p.NodeType, p.PostStyle, p.Stance,
	)
	prompt := fmt.Sprintf(
		"React to this content: '%s'\n\n"+
			`Return JSON only: {"reaction":"share|comment|like|ignore|oppose","response":"your text","sentiment":"positive|neutral|negative","score":1}`,
		truncScenario(copyText),
	)

	var raw struct {
		Reaction  string `json:"reaction"`
		Response  string `json:"response"`
		Sentiment string `json:"sentiment"`
		Score     int    `json:"score"`
	}
	if err := ps.llm.JSON(ctx, system, prompt, &raw); err != nil {
		return CopyReactionResult{}, err
	}

	// Validate reaction
	switch raw.Reaction {
	case "share", "comment", "like", "ignore", "oppose":
	default:
		raw.Reaction = "ignore"
	}
	// Validate sentiment
	switch raw.Sentiment {
	case "positive", "neutral", "negative":
	default:
		raw.Sentiment = "neutral"
	}
	// Clamp score
	if raw.Score < 1 {
		raw.Score = 1
	}
	if raw.Score > 10 {
		raw.Score = 10
	}

	return CopyReactionResult{
		Reaction:  raw.Reaction,
		Response:  raw.Response,
		Sentiment: raw.Sentiment,
		Score:     raw.Score,
	}, nil
}

// ─── RunCopyReactFromProject ──────────────────────────────────────────────────

// RunCopyReactFromProject is the high-level entry point for the CLI.
// It loads nodes from the DB, builds default personalities, then calls SimulateCopyReactions.
func (ps *PlatformSim) RunCopyReactFromProject(
	ctx context.Context,
	projectID string,
	copyConfig CopyReactionConfig,
	maxAgents int,
) ([]CopyReactionResult, error) {
	nodes, err := ps.db.GetNodes(projectID)
	if err != nil {
		return nil, fmt.Errorf("get nodes: %w", err)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes found; run: fishnet analyze first")
	}
	if maxAgents > 0 && len(nodes) > maxAgents {
		nodes = nodes[:maxAgents]
	}

	personalities := make([]*platform.Personality, len(nodes))
	for i, n := range nodes {
		personalities[i] = platform.FromNode(n, i)
	}

	return ps.SimulateCopyReactions(ctx, projectID, copyConfig, personalities, nil)
}

// ─── FormatCopyReactions ──────────────────────────────────────────────────────

// FormatCopyReactions renders copy reaction results as human-readable text.
func FormatCopyReactions(results []CopyReactionResult) string {
	out := ""
	for _, r := range results {
		sentEmoji := "~"
		switch r.Sentiment {
		case "positive":
			sentEmoji = "+"
		case "negative":
			sentEmoji = "-"
		}
		out += fmt.Sprintf("  [%s %2d/10] %-20s %-8s %s\n",
			sentEmoji, r.Score, r.AgentName, r.Reaction, r.Response)
	}
	return out
}
