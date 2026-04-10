package sim

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"fishnet/internal/platform"
)

// ─── SimMetrics ───────────────────────────────────────────────────────────────

// SimMetrics holds all computed diffusion and influence metrics for a simulation.
type SimMetrics struct {
	// Branching Factor (R0) — average number of secondary reposts per source post.
	// R0 > 1 means content is spreading exponentially.
	BranchingFactor float64

	// BranchingByRound is R0 computed per round.
	BranchingByRound []float64

	// KCoreReach records what fraction of all agents each k-core level reached.
	// Index 0 = k=1 (all agents who interacted), index 1 = k=2, etc.
	KCoreReach []KCoreLevel

	// SentimentPolarity measures divergence between positive and negative engagement
	// per round (0 = unified, 1 = fully polarised).
	SentimentPolarity []float64

	// TimeToPeak is the distribution of how many rounds it took each post to reach
	// its maximum interaction count (indexed by round 1..MaxRounds).
	TimeToPeak []TimeToPeakBucket

	// TopInfluencers lists the agents whose posts generated the most secondary reposts.
	TopInfluencers []InfluencerStat

	// MaxRounds is the total number of rounds in the simulation.
	MaxRounds int

	// TotalPosts, TotalReposts, TotalLikes for summary.
	TotalPosts   int
	TotalReposts int
	TotalLikes   int
}

// KCoreLevel holds the k-core penetration result for a given k value.
type KCoreLevel struct {
	K            int
	AgentCount   int     // number of agents with degree ≥ k
	FractionOfAll float64 // fraction of all interacting agents
}

// TimeToPeakBucket counts how many posts peaked at a given round.
type TimeToPeakBucket struct {
	Round int
	Count int
}

// InfluencerStat is one agent's repost-driving score.
type InfluencerStat struct {
	AgentName      string
	Stance         string
	SecondaryPosts int // reposts/quotes triggered by their original posts
}

// ─── ComputeMetrics ───────────────────────────────────────────────────────────

// ComputeMetrics derives all SimMetrics from a completed simulation's action log.
func ComputeMetrics(actions []platform.Action, personalities []*platform.Personality) SimMetrics {
	if len(actions) == 0 {
		return SimMetrics{}
	}

	// ── Build lookup maps ─────────────────────────────────────────────────────
	stanceByAgent := make(map[string]string, len(personalities))
	for _, p := range personalities {
		if p != nil {
			stanceByAgent[p.AgentID] = p.Stance
		}
	}

	maxRound := 0
	for _, a := range actions {
		if a.Round > maxRound {
			maxRound = a.Round
		}
	}

	// ── Index: postID → {author, round, reposts, likes, dislikes per round} ──
	type postStats struct {
		authorID     string
		authorName   string
		createdRound int
		// repostsByRound[r] = number of reposts in round r
		repostsByRound map[int]int
		likesByRound   map[int]int
		dislikesByRound map[int]int
	}
	postIndex := make(map[string]*postStats)

	// First pass: create entries for all posts
	for _, a := range actions {
		if a.PostID == "" {
			continue
		}
		if a.Type == platform.ActCreatePost || a.Type == platform.ActCreateComment ||
			a.Type == platform.ActQuotePost {
			if _, ok := postIndex[a.PostID]; !ok {
				postIndex[a.PostID] = &postStats{
					authorID:        a.AgentID,
					authorName:      a.AgentName,
					createdRound:    a.Round,
					repostsByRound:  make(map[int]int),
					likesByRound:    make(map[int]int),
					dislikesByRound: make(map[int]int),
				}
			}
		}
	}

	// Second pass: tally interactions
	roundPosts := make(map[int]int)
	roundReposts := make(map[int]int)
	roundLikes := make(map[int]int)
	roundDislikes := make(map[int]int)

	for _, a := range actions {
		switch a.Type {
		case platform.ActCreatePost:
			roundPosts[a.Round]++
		case platform.ActRepost:
			roundReposts[a.Round]++
			if ps, ok := postIndex[a.PostID]; ok {
				ps.repostsByRound[a.Round]++
			}
		case platform.ActLikePost, platform.ActLikeComment:
			roundLikes[a.Round]++
			if ps, ok := postIndex[a.PostID]; ok {
				ps.likesByRound[a.Round]++
			}
		case platform.ActDislikePost, platform.ActDislikeComment:
			roundDislikes[a.Round]++
			if ps, ok := postIndex[a.PostID]; ok {
				ps.dislikesByRound[a.Round]++
			}
		case platform.ActQuotePost:
			// Quote counts as a repost (secondary content creation)
			roundReposts[a.Round]++
			if ps, ok := postIndex[a.PostID]; ok {
				ps.repostsByRound[a.Round]++
			}
		}
	}

	// ── 1. Branching Factor (R0) by round ─────────────────────────────────────
	branchingByRound := make([]float64, maxRound+1)
	for r := 1; r <= maxRound; r++ {
		posts := roundPosts[r]
		reposts := roundReposts[r]
		if posts > 0 {
			branchingByRound[r] = float64(reposts) / float64(posts)
		}
	}
	overallR0 := 0.0
	totalPosts := sumMap(roundPosts)
	totalReposts := sumMap(roundReposts)
	if totalPosts > 0 {
		overallR0 = float64(totalReposts) / float64(totalPosts)
	}

	// ── 2. K-core penetration ─────────────────────────────────────────────────
	// Build agent interaction degree graph: for each agent, count distinct agents
	// whose posts they interacted with (liked/reposted/commented on).
	agentDegree := make(map[string]map[string]bool) // agentID → set of authors interacted with
	for _, a := range actions {
		if a.PostID == "" {
			continue
		}
		switch a.Type {
		case platform.ActLikePost, platform.ActRepost, platform.ActDislikePost,
			platform.ActQuotePost, platform.ActCreateComment:
			if ps, ok := postIndex[a.PostID]; ok && ps.authorID != a.AgentID {
				if agentDegree[a.AgentID] == nil {
					agentDegree[a.AgentID] = make(map[string]bool)
				}
				agentDegree[a.AgentID][ps.authorID] = true
			}
		}
	}

	// Compute in-degree (how many distinct agents interacted with each agent's posts)
	inDegree := make(map[string]int)
	for viewer, authors := range agentDegree {
		for author := range authors {
			_ = viewer
			inDegree[author]++
		}
	}

	// K-core: iterative pruning (compute for k=1,2,3,4,5)
	kCoreReach := make([]KCoreLevel, 0, 5)
	totalInteracting := len(inDegree)
	for k := 1; k <= 5; k++ {
		count := 0
		for _, deg := range inDegree {
			if deg >= k {
				count++
			}
		}
		if count == 0 {
			break
		}
		fraction := 0.0
		if totalInteracting > 0 {
			fraction = float64(count) / float64(totalInteracting)
		}
		kCoreReach = append(kCoreReach, KCoreLevel{K: k, AgentCount: count, FractionOfAll: fraction})
	}

	// ── 3. Sentiment Polarity by round ─────────────────────────────────────────
	// Polarity = |likes - dislikes| / (likes + dislikes + 1), normalized to [0,1].
	// High polarity = heavily one-sided; balanced engagement = low polarity.
	sentimentPolarity := make([]float64, maxRound+1)
	for r := 1; r <= maxRound; r++ {
		likes := float64(roundLikes[r])
		dislikes := float64(roundDislikes[r])
		total := likes + dislikes
		if total > 0 {
			// Divergence: 0 = perfect balance, 1 = all likes or all dislikes
			sentimentPolarity[r] = math.Abs(likes-dislikes) / total
		}
	}

	// ── 4. Time-to-Peak ───────────────────────────────────────────────────────
	// For each post, find the round with the most combined interactions.
	peakRoundCounts := make(map[int]int)
	for _, ps := range postIndex {
		peakRound := ps.createdRound
		peakVal := 0
		for r := 1; r <= maxRound; r++ {
			v := ps.repostsByRound[r] + ps.likesByRound[r]
			if v > peakVal {
				peakVal = v
				peakRound = r
			}
		}
		if peakVal > 0 {
			lag := peakRound - ps.createdRound
			if lag < 0 {
				lag = 0
			}
			peakRoundCounts[lag]++
		}
	}
	// Convert to sorted buckets (by lag rounds)
	lags := make([]int, 0, len(peakRoundCounts))
	for lag := range peakRoundCounts {
		lags = append(lags, lag)
	}
	sort.Ints(lags)
	timeToPeak := make([]TimeToPeakBucket, 0, len(lags))
	for _, lag := range lags {
		timeToPeak = append(timeToPeak, TimeToPeakBucket{Round: lag, Count: peakRoundCounts[lag]})
	}

	// ── 5. Top influencers ────────────────────────────────────────────────────
	type influencerData struct {
		name     string
		secondary int
	}
	infMap := make(map[string]*influencerData)
	for _, ps := range postIndex {
		total := 0
		for _, v := range ps.repostsByRound {
			total += v
		}
		if total == 0 {
			continue
		}
		if infMap[ps.authorID] == nil {
			infMap[ps.authorID] = &influencerData{name: ps.authorName}
		}
		infMap[ps.authorID].secondary += total
	}
	infList := make([]InfluencerStat, 0, len(infMap))
	for id, d := range infMap {
		infList = append(infList, InfluencerStat{
			AgentName:      d.name,
			Stance:         stanceByAgent[id],
			SecondaryPosts: d.secondary,
		})
	}
	sort.Slice(infList, func(i, j int) bool {
		return infList[i].SecondaryPosts > infList[j].SecondaryPosts
	})
	if len(infList) > 10 {
		infList = infList[:10]
	}

	return SimMetrics{
		BranchingFactor:   overallR0,
		BranchingByRound:  branchingByRound[1:], // strip round-0 padding
		KCoreReach:        kCoreReach,
		SentimentPolarity: sentimentPolarity[1:],
		TimeToPeak:        timeToPeak,
		TopInfluencers:    infList,
		MaxRounds:         maxRound,
		TotalPosts:        totalPosts,
		TotalReposts:      totalReposts,
		TotalLikes:        sumMap(roundLikes),
	}
}

// ─── RenderMetricsReport ──────────────────────────────────────────────────────

// RenderMetricsReport renders SimMetrics to a Markdown string with ASCII charts.
// The output is intended to be appended to the main simulation report.
func RenderMetricsReport(m SimMetrics) string {
	var sb strings.Builder

	sb.WriteString("## Simulation Analytics\n\n")

	// ── Summary box ───────────────────────────────────────────────────────────
	sb.WriteString("### Summary\n\n")
	sb.WriteString("| Metric | Value |\n|--------|-------|\n")
	sb.WriteString(fmt.Sprintf("| Total Posts | %d |\n", m.TotalPosts))
	sb.WriteString(fmt.Sprintf("| Total Reposts | %d |\n", m.TotalReposts))
	sb.WriteString(fmt.Sprintf("| Total Likes | %d |\n", m.TotalLikes))
	sb.WriteString(fmt.Sprintf("| Branching Factor (R₀) | **%.2f** |\n", m.BranchingFactor))
	if m.BranchingFactor >= 1.5 {
		sb.WriteString("| Spread Assessment | 🔥 Viral (R₀ ≥ 1.5) |\n")
	} else if m.BranchingFactor >= 1.0 {
		sb.WriteString("| Spread Assessment | 📈 Growing (R₀ ≥ 1.0) |\n")
	} else {
		sb.WriteString("| Spread Assessment | 📉 Contained (R₀ < 1.0) |\n")
	}
	sb.WriteString("\n")

	// ── 1. Branching Factor by Round ─────────────────────────────────────────
	sb.WriteString("### 1. Branching Factor (R₀) by Round\n\n")
	sb.WriteString("_Average secondary reposts per original post each round. R₀ > 1 = exponential spread._\n\n")
	sb.WriteString("```\n")
	maxBF := 0.0
	for _, v := range m.BranchingByRound {
		if v > maxBF {
			maxBF = v
		}
	}
	if maxBF == 0 {
		maxBF = 1
	}
	for i, v := range m.BranchingByRound {
		bar := asciiBar(v, maxBF, 30)
		threshold := " "
		if v >= 1.0 {
			threshold = "▶"
		}
		sb.WriteString(fmt.Sprintf("R%-2d %s %s %.2f\n", i+1, threshold, bar, v))
	}
	sb.WriteString("```\n\n")

	// ── 2. K-Core Penetration ─────────────────────────────────────────────────
	sb.WriteString("### 2. K-Core Penetration\n\n")
	sb.WriteString("_How deep content spread into core users. k-1 = fringe, k-3+ = core community._\n\n")
	sb.WriteString("```\n")
	for _, level := range m.KCoreReach {
		bar := asciiBar(level.FractionOfAll, 1.0, 30)
		sb.WriteString(fmt.Sprintf("k=%-2d %s %.0f%% of active agents (%d agents)\n",
			level.K, bar, level.FractionOfAll*100, level.AgentCount))
	}
	if len(m.KCoreReach) == 0 {
		sb.WriteString("(insufficient interaction data)\n")
	}
	sb.WriteString("```\n\n")

	// ── 3. Sentiment Polarity ─────────────────────────────────────────────────
	sb.WriteString("### 3. Sentiment Polarity by Round\n\n")
	sb.WriteString("_0 = balanced engagement, 1.0 = fully one-sided. Rising = polarization trend._\n\n")
	sb.WriteString("```\n")
	for i, v := range m.SentimentPolarity {
		bar := asciiBar(v, 1.0, 30)
		label := "balanced"
		if v >= 0.7 {
			label = "polarised"
		} else if v >= 0.4 {
			label = "leaning"
		}
		sb.WriteString(fmt.Sprintf("R%-2d %s %.2f  %s\n", i+1, bar, v, label))
	}
	if len(m.SentimentPolarity) == 0 {
		sb.WriteString("(no sentiment data)\n")
	}
	sb.WriteString("```\n\n")

	// ── 4. Time-to-Peak ───────────────────────────────────────────────────────
	sb.WriteString("### 4. Time-to-Peak Distribution\n\n")
	sb.WriteString("_Rounds from post creation to peak interaction. 0 = immediate, high = slow burn._\n\n")
	sb.WriteString("```\n")
	maxTTP := 0
	for _, b := range m.TimeToPeak {
		if b.Count > maxTTP {
			maxTTP = b.Count
		}
	}
	if maxTTP == 0 {
		maxTTP = 1
	}
	for _, b := range m.TimeToPeak {
		bar := asciiBar(float64(b.Count), float64(maxTTP), 30)
		lag := b.Round
		label := fmt.Sprintf("+%d rounds", lag)
		if lag == 0 {
			label = "same round"
		} else if lag == 1 {
			label = "+1 round"
		}
		sb.WriteString(fmt.Sprintf("%-12s %s %d posts\n", label, bar, b.Count))
	}
	if len(m.TimeToPeak) == 0 {
		sb.WriteString("(no posts with tracked interactions)\n")
	}
	sb.WriteString("```\n\n")

	// ── 5. Top Influencers ────────────────────────────────────────────────────
	sb.WriteString("### 5. Top Influencers by Secondary Spread\n\n")
	sb.WriteString("_Agents whose posts generated the most reposts / quote-posts._\n\n")
	if len(m.TopInfluencers) == 0 {
		sb.WriteString("_No repost data available._\n\n")
	} else {
		sb.WriteString("| Rank | Agent | Stance | Secondary Posts |\n")
		sb.WriteString("|------|-------|--------|-----------------|\n")
		maxSec := m.TopInfluencers[0].SecondaryPosts
		for i, inf := range m.TopInfluencers {
			bar := asciiBar(float64(inf.SecondaryPosts), float64(maxSec), 15)
			sb.WriteString(fmt.Sprintf("| %d | **%s** | %s | %s %d |\n",
				i+1, inf.AgentName, inf.Stance, bar, inf.SecondaryPosts))
		}
		sb.WriteString("\n")
	}

	// ── Interpretation ────────────────────────────────────────────────────────
	sb.WriteString("### Interpretation\n\n")
	sb.WriteString(interpretMetrics(m))
	sb.WriteString("\n")

	return sb.String()
}

// interpretMetrics generates a plain-language summary of what the metrics mean.
func interpretMetrics(m SimMetrics) string {
	var parts []string

	// R0 interpretation
	if m.BranchingFactor >= 1.5 {
		parts = append(parts, fmt.Sprintf(
			"**Content spread virally** with R₀=%.2f — each post triggered %.1f secondary reposts on average. "+
				"This indicates high audience resonance and organic amplification.",
			m.BranchingFactor, m.BranchingFactor))
	} else if m.BranchingFactor >= 1.0 {
		parts = append(parts, fmt.Sprintf(
			"**Content spread sustainably** with R₀=%.2f — slightly above the viral threshold. "+
				"Targeted seeding of key influencers could push this into exponential territory.",
			m.BranchingFactor))
	} else {
		parts = append(parts, fmt.Sprintf(
			"**Content spread was contained** (R₀=%.2f). Each post was shared fewer times than it was created, "+
				"suggesting the narrative did not resonate strongly or lacked sufficient seeding.",
			m.BranchingFactor))
	}

	// K-core interpretation
	if len(m.KCoreReach) >= 3 {
		k3 := m.KCoreReach[2]
		parts = append(parts, fmt.Sprintf(
			"**Core community penetration (k=3)**: %.0f%% of active agents became deeply embedded in the conversation, "+
				"each interacting with 3+ distinct peers. This suggests the narrative reached genuine community insiders.",
			k3.FractionOfAll*100))
	} else if len(m.KCoreReach) >= 2 {
		parts = append(parts, "Content reached k=2 core users but did not penetrate deeper community clusters. "+
			"Stronger seeding or more provocative framing may be needed to activate core audiences.")
	}

	// Polarity interpretation
	if len(m.SentimentPolarity) > 0 {
		avgPolarity := 0.0
		for _, v := range m.SentimentPolarity {
			avgPolarity += v
		}
		avgPolarity /= float64(len(m.SentimentPolarity))

		// Check for trend
		trend := "stable"
		if len(m.SentimentPolarity) >= 3 {
			first := m.SentimentPolarity[0]
			last := m.SentimentPolarity[len(m.SentimentPolarity)-1]
			if last-first > 0.15 {
				trend = "increasing (polarising)"
			} else if first-last > 0.15 {
				trend = "decreasing (converging)"
			}
		}
		parts = append(parts, fmt.Sprintf(
			"**Sentiment polarity** averaged %.2f (trend: %s). %s",
			avgPolarity, trend,
			polarityAdvice(avgPolarity)))
	}

	// Time-to-Peak interpretation
	if len(m.TimeToPeak) > 0 {
		immediate := 0
		total := 0
		for _, b := range m.TimeToPeak {
			total += b.Count
			if b.Round <= 1 {
				immediate += b.Count
			}
		}
		pct := 0.0
		if total > 0 {
			pct = float64(immediate) / float64(total) * 100
		}
		parts = append(parts, fmt.Sprintf(
			"**%.0f%% of posts peaked within 1 round** of creation — indicating a fast news-cycle dynamic "+
				"where early engagement is critical. Content that does not spark immediate reactions is unlikely to resurface.",
			pct))
	}

	return strings.Join(parts, "\n\n") + "\n"
}

func polarityAdvice(avg float64) string {
	if avg >= 0.6 {
		return "High polarisation: the audience is splitting into strongly like/dislike camps. " +
			"This can amplify reach via controversy but risks entrenching opposition."
	} else if avg >= 0.35 {
		return "Moderate polarity: some resistance exists but the narrative is not deeply divisive. " +
			"Reframing toward common values could reduce opposition."
	}
	return "Low polarity: the audience largely agrees (or is indifferent). " +
		"This is stable but may indicate limited engagement breadth."
}

// ─── ASCII chart helpers ──────────────────────────────────────────────────────

// asciiBar renders a filled bar of width proportional to value/max.
func asciiBar(value, max float64, width int) string {
	if max <= 0 || width <= 0 {
		return strings.Repeat("░", width)
	}
	filled := int(math.Round(value / max * float64(width)))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func sumMap(m map[int]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}
