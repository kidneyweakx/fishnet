package sim

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"fishnet/internal/llm"
	"fishnet/internal/platform"
)

// ─── Export types ─────────────────────────────────────────────────────────────

// ExportInput holds all data from a completed simulation.
type ExportInput struct {
	Scenario      string
	Rounds        int
	Actions       []platform.Action
	Personalities []*platform.Personality // may be nil
}

// ExportDoc is the generated output artifact.
type ExportDoc struct {
	BDReport    string            `json:"bd_report"`    // markdown
	KeyAgents   string            `json:"key_agents"`   // markdown
	Tactics     string            `json:"tactics"`      // markdown
	TopQuotes   []PostQuote       `json:"top_quotes"`   // twitter/reddit highlights
	AgentQuotes map[string]string `json:"agent_quotes"` // agent name → best quote
}

// PostQuote is a highlighted post with its engagement stats.
type PostQuote struct {
	PostID    string `json:"post_id"`
	Agent     string `json:"agent"`
	Platform  string `json:"platform"`
	Content   string `json:"content"`
	Round     int    `json:"round"`
	Reactions int    `json:"reactions"` // like + repost actions referencing this post
}

// ─── GenerateExport ───────────────────────────────────────────────────────────

// GenerateExport analyzes simulation actions and produces a full export document.
// It makes 1 LLM call for the strategic documents.
func GenerateExport(ctx context.Context, client *llm.Client, input *ExportInput) (*ExportDoc, error) {
	// 1. Compute post engagement from actions
	postIndex := buildPostIndex(input.Actions)

	// 2. Top quotes: posts sorted by reactions
	topQuotes := topPostQuotes(postIndex, 15)

	// 3. Agent best quote: highest-reaction post per agent
	agentQuotes := agentBestQuotes(postIndex, topQuotes)

	// 4. Build compact simulation summary for LLM
	summary := buildSimSummary(input, topQuotes)

	// 5. Single LLM call → all three strategic documents
	type analysisResult struct {
		BDReport  string `json:"bd_report"`
		KeyAgents string `json:"key_agents"`
		Tactics   string `json:"tactics"`
	}

	const sys = `You are a strategic communications analyst. Analyze the social media simulation data and produce three documents in JSON.
Return exactly: {"bd_report":"...","key_agents":"...","tactics":"..."}
Each field must be a markdown string.

bd_report: Executive summary, audience insights, message resonance, key findings (500-700 words).
key_agents: Identify and rank the most influential simulation agents with evidence from their posts (300-400 words).
tactics: "If you want this narrative to develop further" — concrete tactical recommendations, core methods, intervention points (300-400 words).`

	prompt := fmt.Sprintf("Scenario: %s\nRounds: %d\n\n%s", input.Scenario, input.Rounds, summary)

	var result analysisResult
	if err := client.JSON(ctx, sys, prompt, &result); err != nil {
		return nil, fmt.Errorf("export llm: %w", err)
	}

	return &ExportDoc{
		BDReport:    result.BDReport,
		KeyAgents:   result.KeyAgents,
		Tactics:     result.Tactics,
		TopQuotes:   topQuotes,
		AgentQuotes: agentQuotes,
	}, nil
}

// ─── Post index ───────────────────────────────────────────────────────────────

type postRecord struct {
	quote PostQuote
	likes int
	reposts int
}

func buildPostIndex(actions []platform.Action) map[string]*postRecord {
	idx := make(map[string]*postRecord)

	// First pass: index all created posts
	for _, a := range actions {
		if a.Content == "" || a.PostID == "" {
			continue
		}
		if _, ok := idx[a.PostID]; !ok {
			idx[a.PostID] = &postRecord{
				quote: PostQuote{
					PostID:   a.PostID,
					Agent:    a.AgentName,
					Platform: a.Platform,
					Content:  a.Content,
					Round:    a.Round,
				},
			}
		}
	}

	// Second pass: count reactions
	for _, a := range actions {
		if a.PostID == "" {
			continue
		}
		rec, ok := idx[a.PostID]
		if !ok {
			continue
		}
		switch a.Type {
		case platform.ActLikePost, platform.ActLikeComment:
			rec.likes++
		case platform.ActDislikePost, platform.ActDislikeComment:
			rec.likes-- // net negative engagement
		case platform.ActRepost:
			rec.reposts++
		}
	}

	// Set reaction totals
	for _, rec := range idx {
		rec.quote.Reactions = rec.likes + rec.reposts*2 // reposts weighted 2x
	}
	return idx
}

func topPostQuotes(idx map[string]*postRecord, n int) []PostQuote {
	posts := make([]PostQuote, 0, len(idx))
	for _, rec := range idx {
		posts = append(posts, rec.quote)
	}
	sort.Slice(posts, func(i, j int) bool {
		return posts[i].Reactions > posts[j].Reactions
	})
	if len(posts) > n {
		posts = posts[:n]
	}
	return posts
}

func agentBestQuotes(idx map[string]*postRecord, top []PostQuote) map[string]string {
	// Pick the highest-reaction post per agent from the top quotes
	seen := make(map[string]bool)
	result := make(map[string]string)
	for _, q := range top {
		if !seen[q.Agent] && q.Content != "" {
			result[q.Agent] = q.Content
			seen[q.Agent] = true
		}
	}
	return result
}

// ─── Summary builder ──────────────────────────────────────────────────────────

func buildSimSummary(input *ExportInput, topQuotes []PostQuote) string {
	var sb strings.Builder

	// Action type counts
	typeCounts := make(map[string]int)
	platformCounts := make(map[string]int)
	agentActivity := make(map[string]int)
	for _, a := range input.Actions {
		typeCounts[a.Type]++
		platformCounts[a.Platform]++
		agentActivity[a.AgentName]++
	}

	sb.WriteString(fmt.Sprintf("Total actions: %d\n", len(input.Actions)))
	sb.WriteString(fmt.Sprintf("Action breakdown: %s\n", formatCounts(typeCounts)))
	sb.WriteString(fmt.Sprintf("Platform split: %s\n", formatCounts(platformCounts)))

	// Most active agents
	type agentStat struct{ name string; count int }
	stats := make([]agentStat, 0, len(agentActivity))
	for name, c := range agentActivity {
		stats = append(stats, agentStat{name, c})
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].count > stats[j].count })
	top := stats
	if len(top) > 8 {
		top = top[:8]
	}
	sb.WriteString("Most active agents: ")
	for i, s := range top {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("%s(%d)", s.name, s.count))
	}
	sb.WriteString("\n\n")

	// Top posts sample
	sb.WriteString("Highest-engagement posts:\n")
	for i, q := range topQuotes {
		if i >= 8 {
			break
		}
		sb.WriteString(fmt.Sprintf("  [%s @%s r%d reactions:%d] %s\n",
			q.Platform, q.Agent, q.Round, q.Reactions, clip(q.Content, 120)))
	}

	// Personality stances (if available)
	if len(input.Personalities) > 0 {
		stances := make(map[string]int)
		for _, p := range input.Personalities {
			if p != nil {
				stances[p.Stance]++
			}
		}
		sb.WriteString(fmt.Sprintf("\nAgent stances: %s\n", formatCounts(stances)))
	}

	return sb.String()
}

func formatCounts(m map[string]int) string {
	type kv struct{ k string; v int }
	pairs := make([]kv, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf("%s:%d", p.k, p.v))
	}
	return strings.Join(parts, " ")
}

// ─── Save helpers ─────────────────────────────────────────────────────────────

// SaveExport writes the export document to a directory.
func SaveExport(doc *ExportDoc, dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// bd-report.md
	if err := os.WriteFile(dir+"/bd-report.md", []byte(doc.BDReport), 0644); err != nil {
		return err
	}
	// key-agents.md
	if err := os.WriteFile(dir+"/key-agents.md", []byte(doc.KeyAgents), 0644); err != nil {
		return err
	}
	// tactics.md
	if err := os.WriteFile(dir+"/tactics.md", []byte(doc.Tactics), 0644); err != nil {
		return err
	}

	// quotes.json
	quotesJSON, _ := json.MarshalIndent(map[string]interface{}{
		"top_quotes":   doc.TopQuotes,
		"agent_quotes": doc.AgentQuotes,
	}, "", "  ")
	if err := os.WriteFile(dir+"/quotes.json", quotesJSON, 0644); err != nil {
		return err
	}

	// full export.json
	fullJSON, _ := json.MarshalIndent(doc, "", "  ")
	return os.WriteFile(dir+"/export.json", fullJSON, 0644)
}

// ─── LoadActions ──────────────────────────────────────────────────────────────

// LoadActionsFromJSONL reads platform.Action records from an actions.jsonl file.
func LoadActionsFromJSONL(path string) ([]platform.Action, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var actions []platform.Action
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var a platform.Action
		if err := json.Unmarshal([]byte(line), &a); err == nil {
			actions = append(actions, a)
		}
	}
	return actions, sc.Err()
}
