package sim

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"fishnet/internal/db"
	"fishnet/internal/llm"
)

// ─── Types ────────────────────────────────────────────────────────────────────

type AgentResponse struct {
	AgentName string `json:"agent"`
	AgentType string `json:"type"`
	Response  string `json:"response"`
	Sentiment string `json:"sentiment"` // positive | negative | neutral
	Score     int    `json:"score"`     // 1-10 resonance score
}

type SimResult struct {
	Scenario  string          `json:"scenario"`
	Responses []AgentResponse `json:"responses"`
	Summary   string          `json:"summary"`
	TopCopy   []string        `json:"top_copy,omitempty"`
}

// ─── Engine ───────────────────────────────────────────────────────────────────

type Engine struct {
	db  *db.DB
	llm *llm.Client
}

func New(database *db.DB, client *llm.Client) *Engine {
	return &Engine{db: database, llm: client}
}

// RunCopySimulation simulates how graph entities (as personas) react to a scenario/copy draft.
func (e *Engine) RunCopySimulation(
	ctx context.Context,
	projectID, scenario string,
	maxAgents int,
	onAgent func(AgentResponse),
) (*SimResult, error) {

	nodes, err := e.db.GetNodes(projectID)
	if err != nil {
		return nil, fmt.Errorf("get nodes: %w", err)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes found; run: fishnet analyze first")
	}

	// Filter to person/org nodes and cap at maxAgents
	agents := filterAgents(nodes, maxAgents)
	if len(agents) == 0 {
		agents = nodes
		if len(agents) > maxAgents {
			agents = agents[:maxAgents]
		}
	}

	result := &SimResult{Scenario: scenario}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5) // max 5 concurrent persona calls

	for _, node := range agents {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(n db.Node) {
			defer func() { <-sem; wg.Done() }()

			resp, err := e.askPersona(ctx, n, scenario)
			if err != nil {
				return
			}
			mu.Lock()
			result.Responses = append(result.Responses, resp)
			mu.Unlock()
			if onAgent != nil {
				onAgent(resp)
			}
		}(node)
	}
	wg.Wait()

	// Synthesize summary
	if len(result.Responses) > 0 {
		result.Summary, _ = e.synthesize(ctx, scenario, result.Responses)
	}
	return result, nil
}

// GenerateCopy generates N copy variants based on simulation feedback.
func (e *Engine) GenerateCopy(
	ctx context.Context,
	scenario, style string,
	count int,
	responses []AgentResponse,
) ([]string, error) {

	var feedback strings.Builder
	for i, r := range responses {
		if i >= 20 {
			break
		}
		fmt.Fprintf(&feedback, "- %s (%s, score=%d): %s\n", r.AgentName, r.Sentiment, r.Score, r.Response)
	}

	prompt := fmt.Sprintf(`Based on this copywriting brief and the audience feedback below, generate %d distinct copy variants.
Style: %s
Brief: %s

Audience feedback:
%s

Return JSON:
{"variants": ["copy1", "copy2", ...]}`, count, style, scenario, feedback.String())

	var out struct {
		Variants []string `json:"variants"`
	}
	err := e.llm.JSON(ctx,
		"You are an expert copywriter. Generate distinct, compelling copy variants.",
		prompt, &out)
	if err != nil {
		return nil, err
	}
	return out.Variants, nil
}

// ─── Internal ────────────────────────────────────────────────────────────────

func (e *Engine) askPersona(ctx context.Context, node db.Node, scenario string) (AgentResponse, error) {
	system := fmt.Sprintf(`You are %s, a %s. %s
Respond as this persona to the following prompt. Be specific and authentic to this persona.
Return JSON: {"response": "your reaction", "sentiment": "positive|negative|neutral", "score": 1-10}
Where score = how much this resonates with you (10=highly relevant, 1=irrelevant).`,
		node.Name, node.Type, node.Summary)

	var raw struct {
		Response  string `json:"response"`
		Sentiment string `json:"sentiment"`
		Score     int    `json:"score"`
	}

	err := e.llm.JSON(ctx, system, scenario, &raw)
	if err != nil {
		return AgentResponse{}, err
	}

	return AgentResponse{
		AgentName: node.Name,
		AgentType: node.Type,
		Response:  raw.Response,
		Sentiment: raw.Sentiment,
		Score:     raw.Score,
	}, nil
}

func (e *Engine) synthesize(ctx context.Context, scenario string, responses []AgentResponse) (string, error) {
	positive, negative, neutral := 0, 0, 0
	var highlights strings.Builder
	for i, r := range responses {
		switch r.Sentiment {
		case "positive":
			positive++
		case "negative":
			negative++
		default:
			neutral++
		}
		if i < 5 {
			fmt.Fprintf(&highlights, "- %s (score %d): %s\n", r.AgentName, r.Score, r.Response)
		}
	}

	total := len(responses)
	prompt := fmt.Sprintf(`Scenario: %s
Results: %d agents responded (%d positive, %d negative, %d neutral)
Key highlights:
%s
Summarize the simulation results and key insights in 2-3 sentences.`,
		scenario, total, positive, negative, neutral, highlights.String())

	return e.llm.System(ctx, "You are a simulation analyst. Be concise and actionable.", prompt)
}

func filterAgents(nodes []db.Node, max int) []db.Node {
	agentTypes := map[string]bool{
		"Person": true, "Company": true, "Organization": true,
		"Brand": true, "User": true, "Customer": true, "Influencer": true,
	}
	var agents []db.Node
	for _, n := range nodes {
		if agentTypes[n.Type] {
			agents = append(agents, n)
			if len(agents) >= max {
				break
			}
		}
	}
	return agents
}

// FormatResult renders a SimResult as human-readable text.
func FormatResult(r *SimResult) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n=== Simulation: %s ===\n\n", r.Scenario)
	fmt.Fprintf(&sb, "Agents: %d\n\n", len(r.Responses))

	pos, neg, neu := 0, 0, 0
	for _, resp := range r.Responses {
		switch resp.Sentiment {
		case "positive":
			pos++
		case "negative":
			neg++
		default:
			neu++
		}
	}
	fmt.Fprintf(&sb, "Sentiment: +%d / -%d / ~%d\n\n", pos, neg, neu)

	if r.Summary != "" {
		fmt.Fprintf(&sb, "Summary:\n%s\n\n", r.Summary)
	}

	fmt.Fprintf(&sb, "Agent Responses:\n")
	for _, resp := range r.Responses {
		emoji := "~"
		if resp.Sentiment == "positive" {
			emoji = "+"
		} else if resp.Sentiment == "negative" {
			emoji = "-"
		}
		fmt.Fprintf(&sb, "  [%s %d/10] %s (%s): %s\n",
			emoji, resp.Score, resp.AgentName, resp.AgentType, resp.Response)
	}

	return sb.String()
}

// SaveResult marshals a SimResult to JSON.
func SaveResult(r *SimResult) (string, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	return string(data), err
}
