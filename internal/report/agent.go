// Package report implements a ReAct-style report agent with real tool-calling.
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"fishnet/internal/db"
	"fishnet/internal/graph"
	"fishnet/internal/llm"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// InterviewResult is the structured output of an agent interview.
type InterviewResult struct {
	AgentName string   `json:"agent_name"`
	Response  string   `json:"response"`
	KeyQuotes []string `json:"key_quotes"` // 2-3 verbatim quotes from the response
}

// InterviewReport aggregates results from multiple agents.
type InterviewReport struct {
	Question string            `json:"question"`
	Results  []InterviewResult `json:"results"`
	Summary  string            `json:"summary"` // LLM synthesis of all responses
}

// Section is one section of the generated report.
type Section struct {
	Index   int
	Title   string
	Focus   string
	Content string
}

// Report is the full generated report.
type Report struct {
	ID        string // unique report ID (matches the .md/.jsonl files)
	ProjectID string
	SimID     string
	Scenario  string
	Sections  []Section
	Summary   string
}

// FormatMarkdown renders the report as a Markdown string.
func (r *Report) FormatMarkdown() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Simulation Report\n\n")
	if r.Scenario != "" {
		fmt.Fprintf(&sb, "**Scenario:** %s\n\n", r.Scenario)
	}
	if r.Summary != "" {
		fmt.Fprintf(&sb, "## Executive Summary\n\n%s\n\n---\n\n", r.Summary)
	}
	for _, s := range r.Sections {
		fmt.Fprintf(&sb, "## %d. %s\n\n%s\n\n", s.Index, s.Title, s.Content)
	}
	return sb.String()
}

// ─── Tool infrastructure ───────────────────────────────────────────────────────

// tool is an executable capability available to the LLM during ReAct loops.
type tool struct {
	Name        string
	Description string
	Execute     func(ctx context.Context, params map[string]interface{}) string
}

// toolCall holds a parsed tool invocation from an LLM response.
type toolCall struct {
	Name       string                 `json:"name"`
	Parameters map[string]interface{} `json:"parameters"`
}

// toolCallRE matches <tool_call>…</tool_call> blocks, non-greedy.
var toolCallRE = regexp.MustCompile(`(?s)<tool_call>(.*?)</tool_call>`)

// parseToolCalls extracts all tool calls from an LLM response.
func parseToolCalls(response string) []toolCall {
	matches := toolCallRE.FindAllStringSubmatch(response, -1)
	var calls []toolCall
	for _, m := range matches {
		raw := strings.TrimSpace(m[1])
		var tc toolCall
		if err := json.Unmarshal([]byte(raw), &tc); err != nil {
			continue
		}
		if tc.Name == "" {
			continue
		}
		calls = append(calls, tc)
	}
	return calls
}

// stripToolCalls removes all <tool_call>…</tool_call> blocks from a string.
func stripToolCalls(s string) string {
	return strings.TrimSpace(toolCallRE.ReplaceAllString(s, ""))
}

// strParam extracts a string parameter, returning "" when absent.
func strParam(params map[string]interface{}, key string) string {
	if v, ok := params[key]; ok {
		switch s := v.(type) {
		case string:
			return s
		default:
			return fmt.Sprintf("%v", s)
		}
	}
	return ""
}

// intParam extracts an int parameter with a default fallback.
func intParam(params map[string]interface{}, key string, def int) int {
	if v, ok := params[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case string:
			var i int
			fmt.Sscanf(n, "%d", &i)
			if i > 0 {
				return i
			}
		}
	}
	return def
}

// strSliceParam extracts a []string parameter.
func strSliceParam(params map[string]interface{}, key string) []string {
	v, ok := params[key]
	if !ok {
		return nil
	}
	switch lst := v.(type) {
	case []interface{}:
		var out []string
		for _, item := range lst {
			out = append(out, fmt.Sprintf("%v", item))
		}
		return out
	case []string:
		return lst
	}
	return nil
}

// ─── Agent ────────────────────────────────────────────────────────────────────

// Agent generates reports and conducts interviews using a ReAct-style tool loop.
type Agent struct {
	db  *db.DB
	llm *llm.Client
}

// New constructs a new Agent.
func New(database *db.DB, client *llm.Client) *Agent {
	return &Agent{db: database, llm: client}
}

// buildTools creates the tool set that the LLM can invoke during a ReAct loop.
// projectID and simID provide the data scope; agent is used for interview calls.
func (a *Agent) buildTools(ctx context.Context, projectID, simID string) []tool {
	return []tool{
		{
			Name:        "insight_forge",
			Description: "Deep analysis: decomposes a question into sub-questions, runs multi-angle graph searches, and synthesises findings.",
			Execute: func(ctx context.Context, params map[string]interface{}) string {
				query := strParam(params, "query")
				if query == "" {
					return "insight_forge: 'query' parameter required"
				}
				result, err := graph.InsightForge(ctx, a.db, a.llm, projectID, query)
				if err != nil {
					return fmt.Sprintf("insight_forge error: %v", err)
				}
				return formatSearchResult(result)
			},
		},
		{
			Name:        "panorama_search",
			Description: "Broad search that returns all nodes matching the query plus their immediate neighbours.",
			Execute: func(ctx context.Context, params map[string]interface{}) string {
				query := strParam(params, "query")
				if query == "" {
					return "panorama_search: 'query' parameter required"
				}
				result := graph.PanoramaSearch(a.db, projectID, query, 20)
				return formatSearchResult(result)
			},
		},
		{
			Name:        "quick_search",
			Description: "Fast keyword search on node names, types, summaries, and edge facts.",
			Execute: func(ctx context.Context, params map[string]interface{}) string {
				query := strParam(params, "query")
				if query == "" {
					return "quick_search: 'query' parameter required"
				}
				limit := intParam(params, "limit", 10)
				result := graph.QuickSearch(a.db, projectID, query, limit)
				return formatSearchResult(result)
			},
		},
		{
			Name:        "interview_agents",
			Description: "Ask a question to named graph agents (or the most relevant ones selected by AI if none specified) and collect their in-character responses with key quotes and a synthesis summary.",
			Execute: func(ctx context.Context, params map[string]interface{}) string {
				topic := strParam(params, "topic")
				if topic == "" {
					topic = strParam(params, "interview_topic")
				}
				if topic == "" {
					return "interview_agents: 'topic' parameter required"
				}
				agentNames := strSliceParam(params, "agent_names")

				maxAgents := 3
				if len(agentNames) > 0 {
					maxAgents = len(agentNames)
				}

				// If specific agent names were requested, seed SelectAgents with only those.
				// Otherwise let InterviewStructured pick from the full pool.
				var report *InterviewReport
				var err error

				if len(agentNames) > 0 {
					// Run a targeted structured interview for the named agents.
					nodes, loadErr := a.db.GetNodes(projectID)
					if loadErr != nil {
						return fmt.Sprintf("interview_agents: could not load nodes: %v", loadErr)
					}
					nameSet := make(map[string]bool, len(agentNames))
					for _, n := range agentNames {
						nameSet[strings.ToLower(n)] = true
					}
					var pool []string
					for _, n := range nodes {
						if nameSet[strings.ToLower(n.Name)] {
							pool = append(pool, n.Name)
						}
					}
					if len(pool) == 0 {
						return "interview_agents: no matching agents found"
					}
					// Interview only those agents, skip agent selection step.
					rawResponses, batchErr := a.InterviewBatch(ctx, projectID, pool, topic)
					if batchErr != nil {
						return fmt.Sprintf("interview_agents: batch error: %v", batchErr)
					}
					results := make([]InterviewResult, 0, len(pool))
					var allBuilder strings.Builder
					for _, name := range pool {
						resp := rawResponses[name]
						quotes := extractKeyQuotes(ctx, a.llm, resp, topic)
						results = append(results, InterviewResult{
							AgentName: name,
							Response:  resp,
							KeyQuotes: quotes,
						})
						fmt.Fprintf(&allBuilder, "%s: %s\n\n", name, resp)
					}
					synthesisPrompt := fmt.Sprintf(
						"Synthesize these agent perspectives on \"%s\" into a 2-3 sentence summary:\n\n%s",
						topic, strings.TrimSpace(allBuilder.String()),
					)
					summary, _ := a.llm.System(ctx,
						"You are a synthesis writer. Summarize multiple agent perspectives concisely.",
						synthesisPrompt,
					)
					report = &InterviewReport{
						Question: topic,
						Results:  results,
						Summary:  summary,
					}
				} else {
					// Use LLM to select the most relevant agents for this topic
					selected, selErr := a.selectAgentsLLM(ctx, projectID, topic, maxAgents)
					if selErr != nil || len(selected) == 0 {
						// Fallback to existing structured interview
						report, err = InterviewStructured(ctx, a.db, a.llm, projectID, topic, maxAgents)
						if err != nil {
							return fmt.Sprintf("interview_agents error: %v", err)
						}
					} else {
						rawResponses, batchErr := a.InterviewBatch(ctx, projectID, selected, topic)
						if batchErr != nil {
							return fmt.Sprintf("interview_agents: batch error: %v", batchErr)
						}
						results := make([]InterviewResult, 0, len(selected))
						var allBuilder strings.Builder
						for _, name := range selected {
							resp := rawResponses[name]
							quotes := extractKeyQuotes(ctx, a.llm, resp, topic)
							results = append(results, InterviewResult{
								AgentName: name,
								Response:  resp,
								KeyQuotes: quotes,
							})
							fmt.Fprintf(&allBuilder, "%s: %s\n\n", name, resp)
						}
						synthesisPrompt := fmt.Sprintf(
							"Synthesize these agent perspectives on \"%s\" into a 2-3 sentence summary:\n\n%s",
							topic, strings.TrimSpace(allBuilder.String()),
						)
						summary, _ := a.llm.System(ctx,
							"You are a synthesis writer. Summarize multiple agent perspectives concisely.",
							synthesisPrompt,
						)
						report = &InterviewReport{
							Question: topic,
							Results:  results,
							Summary:  summary,
						}
					}
				}

				if report == nil || len(report.Results) == 0 {
					return "interview_agents: no results"
				}

				var sb strings.Builder
				for _, r := range report.Results {
					fmt.Fprintf(&sb, "%s: %s\n", r.AgentName, r.Response)
					if len(r.KeyQuotes) > 0 {
						fmt.Fprintf(&sb, "  Key quotes:\n")
						for _, q := range r.KeyQuotes {
							fmt.Fprintf(&sb, "    - %q\n", q)
						}
					}
				}
				if report.Summary != "" {
					fmt.Fprintf(&sb, "\nSummary: %s\n", report.Summary)
				}
				return strings.TrimSpace(sb.String())
			},
		},
		{
			Name:        "get_graph_statistics",
			Description: "Returns high-level counts for the knowledge graph: nodes, edges, communities.",
			Execute: func(ctx context.Context, params map[string]interface{}) string {
				stats := a.db.GetStats(projectID)
				return fmt.Sprintf("Graph statistics:\n  Nodes:       %d\n  Edges:       %d\n  Communities: %d\n  Documents:   %d",
					stats.Nodes, stats.Edges, stats.Communities, stats.Documents)
			},
		},
		{
			Name:        "get_entity_summary",
			Description: "Returns the full summary and connected relationships for a specific named entity.",
			Execute: func(ctx context.Context, params map[string]interface{}) string {
				entityName := strParam(params, "entity_name")
				if entityName == "" {
					return "get_entity_summary: 'entity_name' parameter required"
				}
				result := graph.QuickSearch(a.db, projectID, entityName, 5)
				if len(result.Nodes) == 0 {
					return fmt.Sprintf("No entity found matching %q", entityName)
				}
				var sb strings.Builder
				// Find the best-matching node
				target := result.Nodes[0]
				for _, n := range result.Nodes {
					if strings.EqualFold(n.Name, entityName) {
						target = n
						break
					}
				}
				fmt.Fprintf(&sb, "Entity: %s (%s)\nSummary: %s\n", target.Name, target.Type, target.Summary)
				if len(result.Facts) > 0 {
					fmt.Fprintf(&sb, "\nConnected facts:\n")
					for _, f := range result.Facts {
						fmt.Fprintf(&sb, "  - %s\n", f)
					}
				}
				return strings.TrimSpace(sb.String())
			},
		},
		{
			Name:        "get_simulation_context",
			Description: "Retrieves simulation-specific context for a query. Redirects to insight_forge.",
			Execute: func(ctx context.Context, params map[string]interface{}) string {
				query := strParam(params, "query")
				if query == "" {
					return "get_simulation_context: 'query' parameter required"
				}
				result, err := graph.InsightForge(ctx, a.db, a.llm, projectID, query)
				if err != nil {
					return fmt.Sprintf("get_simulation_context error: %v", err)
				}
				return formatSearchResult(result)
			},
		},
	}
}

// toolByName finds a tool in the slice by name.
func toolByName(tools []tool, name string) *tool {
	for i := range tools {
		if tools[i].Name == name {
			return &tools[i]
		}
	}
	return nil
}

// ─── System prompt ─────────────────────────────────────────────────────────────

const sectionSystemPrompt = `You are a simulation analyst generating a detailed report section.

AVAILABLE TOOLS (use XML tags when you want to call a tool):
<tool_call>{"name": "insight_forge", "parameters": {"query": "your question"}}</tool_call>
<tool_call>{"name": "panorama_search", "parameters": {"query": "topic"}}</tool_call>
<tool_call>{"name": "quick_search", "parameters": {"query": "entity name", "limit": 10}}</tool_call>
<tool_call>{"name": "interview_agents", "parameters": {"topic": "question", "agent_names": ["Alice", "Bob"]}}</tool_call>
<tool_call>{"name": "get_graph_statistics", "parameters": {}}</tool_call>
<tool_call>{"name": "get_entity_summary", "parameters": {"entity_name": "Alice"}}</tool_call>
<tool_call>{"name": "get_simulation_context", "parameters": {"query": "what happened"}}</tool_call>

INSTRUCTIONS:
1. First, use 1-3 tool calls to gather specific evidence from the knowledge graph
2. Then write 3-4 analytical paragraphs in Markdown using the evidence gathered
3. Cite specific entities and facts from the tool results
4. Do NOT include tool calls in your final content — only use them during the research phase
5. When you are ready to write final content, do not include any <tool_call> tags

Section: "%s"
Focus: %s
Scenario: %s`

// ─── ReAct loop ───────────────────────────────────────────────────────────────

// generateSectionWithTools runs a ReAct loop for one section.
// The LLM may invoke tools up to maxIter times before producing final Markdown.
func (a *Agent) generateSectionWithTools(
	ctx context.Context,
	title, focus, scenario, projectID, simID string,
	tools []tool,
	logger *Logger,
) (string, error) {
	const maxIter = 5

	system := fmt.Sprintf(sectionSystemPrompt, title, focus, scenario)

	// Pre-fetch graph context using ranked search so the LLM starts with
	// relevant entities and facts before its first tool call.
	graphCtx := graph.GraphContext(a.db, projectID, focus, 20)

	userMsg := fmt.Sprintf("%sGenerate the \"%s\" section of the report.\nFocus: %s\nScenario: %s\n\nStart by calling 1-3 tools to gather evidence, then write the section content.", graphCtx, title, focus, scenario)

	messages := []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: userMsg},
	}

	for i := 0; i < maxIter; i++ {
		response, err := a.llm.Chat(ctx, messages)
		if err != nil {
			return "", fmt.Errorf("llm chat: %w", err)
		}

		calls := parseToolCalls(response)
		if len(calls) == 0 {
			// No tool calls — this is the final content.
			content := stripToolCalls(response)
			if logger != nil {
				logger.Log(LogEntry{
					Time:    now(),
					Stage:   "section_content",
					Section: title,
					Content: content,
				})
			}
			return content, nil
		}

		// Log that the LLM made tool calls.
		if logger != nil {
			for _, tc := range calls {
				queryStr := strParam(tc.Parameters, "query")
				if queryStr == "" {
					queryStr = strParam(tc.Parameters, "topic")
				}
				if queryStr == "" {
					queryStr = strParam(tc.Parameters, "entity_name")
				}
				logger.Log(LogEntry{
					Time:    now(),
					Stage:   "tool_call",
					Section: title,
					Tool:    tc.Name,
					Query:   queryStr,
					Content: fmt.Sprintf("parameters: %v", tc.Parameters),
				})
			}
		}

		// Append the assistant message that contained tool calls.
		messages = append(messages, llm.Message{Role: "assistant", Content: response})

		// Execute each tool and collect results.
		var toolResults strings.Builder
		for _, tc := range calls {
			t := toolByName(tools, tc.Name)
			var result string
			if t == nil {
				result = fmt.Sprintf("Unknown tool: %s. Available: insight_forge, panorama_search, quick_search, interview_agents, get_graph_statistics, get_entity_summary, get_simulation_context", tc.Name)
			} else {
				result = t.Execute(ctx, tc.Parameters)
			}

			if logger != nil {
				logger.Log(LogEntry{
					Time:    now(),
					Stage:   "tool_result",
					Section: title,
					Tool:    tc.Name,
					Content: truncate(result, 500),
				})
			}

			fmt.Fprintf(&toolResults, "<tool_result tool=%q>\n%s\n</tool_result>\n\n", tc.Name, result)
		}

		// Feed results back as a user message.
		messages = append(messages, llm.Message{
			Role:    "user",
			Content: toolResults.String() + "\nNow write the final section content using the evidence above. Do not include any <tool_call> tags.",
		})
	}

	// Exhausted iterations — run one final call without further tool usage.
	messages = append(messages, llm.Message{
		Role:    "user",
		Content: "Write the final section content now based on the research gathered. No tool calls.",
	})
	response, err := a.llm.Chat(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("llm final chat: %w", err)
	}
	content := stripToolCalls(response)
	if logger != nil {
		logger.Log(LogEntry{
			Time:    now(),
			Stage:   "section_content",
			Section: title,
			Content: content,
		})
	}
	return content, nil
}

// ─── Generate ─────────────────────────────────────────────────────────────────

// Generate produces a full report using tool-calling ReAct loops per section.
// Calls onSection after each section is written. simID may be empty.
func (a *Agent) Generate(
	ctx context.Context,
	projectID, scenario string,
	onSection func(Section),
) (*Report, error) {
	return a.GenerateWithSim(ctx, projectID, "", scenario, onSection)
}

// GenerateWithSim is like Generate but accepts an optional simID for context.
func (a *Agent) GenerateWithSim(
	ctx context.Context,
	projectID, simID, scenario string,
	onSection func(Section),
) (*Report, error) {
	// Create logger.
	reportID := sanitizeID(fmt.Sprintf("%s_%s", scenario, time.Now().Format("20060102_150405")))
	logger, err := NewLogger(".fishnet/reports", reportID)
	if err != nil {
		// Non-fatal: continue without logging.
		logger = nil
	}
	if logger != nil {
		defer logger.Close()
	}

	// ── Phase 1: Plan outline ──────────────────────────────────────────────────
	stats := a.db.GetStats(projectID)
	graphSummary := fmt.Sprintf("%d nodes, %d edges, %d communities", stats.Nodes, stats.Edges, stats.Communities)

	var outline struct {
		Sections []struct {
			Title string `json:"title"`
			Focus string `json:"focus"`
		} `json:"sections"`
	}
	err = a.llm.JSON(ctx,
		`Plan a simulation analysis report. Return JSON: {"sections":[{"title":"...","focus":"one-line guidance"}]}. 4-5 sections.`,
		fmt.Sprintf("Scenario: %s\nGraph: %s", scenario, graphSummary),
		&outline)
	if err != nil || len(outline.Sections) == 0 {
		outline.Sections = []struct {
			Title string `json:"title"`
			Focus string `json:"focus"`
		}{
			{Title: "Executive Summary", Focus: "key findings and outcomes"},
			{Title: "Key Actors", Focus: "main entities and their roles"},
			{Title: "Relationship Dynamics", Focus: "key connections and power structures"},
			{Title: "Simulation Outcomes", Focus: "what happened and why"},
			{Title: "Strategic Insights", Focus: "actionable takeaways"},
		}
	}

	if logger != nil {
		var sectionTitles []string
		for _, s := range outline.Sections {
			sectionTitles = append(sectionTitles, s.Title)
		}
		logger.Log(LogEntry{
			Time:    now(),
			Stage:   "planning",
			Content: fmt.Sprintf("Planned %d sections: %s", len(outline.Sections), strings.Join(sectionTitles, " | ")),
		})
	}

	// ── Phase 2: Build tools ───────────────────────────────────────────────────
	tools := a.buildTools(ctx, projectID, simID)

	// ── Phase 3: Generate sections via ReAct ───────────────────────────────────
	report := &Report{ID: reportID, ProjectID: projectID, SimID: simID, Scenario: scenario}

	for i, sec := range outline.Sections {
		content, err := a.generateSectionWithTools(ctx, sec.Title, sec.Focus, scenario, projectID, simID, tools, logger)
		if err != nil {
			content = fmt.Sprintf("*Could not generate section: %v*", err)
		}
		s := Section{Index: i + 1, Title: sec.Title, Focus: sec.Focus, Content: content}
		report.Sections = append(report.Sections, s)
		if onSection != nil {
			onSection(s)
		}
	}

	// ── Phase 4: Executive summary ─────────────────────────────────────────────
	var titles []string
	for _, s := range report.Sections {
		titles = append(titles, s.Title)
	}
	report.Summary, _ = a.llm.System(ctx,
		"Write a 2-sentence executive summary. Be crisp and specific.",
		fmt.Sprintf("Scenario: %s\nReport covers: %s", scenario, strings.Join(titles, " | ")))

	// ── Phase 5: Persist report Markdown ──────────────────────────────────────
	md := report.FormatMarkdown()
	mdPath := fmt.Sprintf(".fishnet/reports/%s.md", reportID)
	if logger != nil {
		// Best-effort write.
		writeReportFile(mdPath, md)
		logger.Log(LogEntry{
			Time:    now(),
			Stage:   "complete",
			Content: fmt.Sprintf("Report written to %s", mdPath),
		})
	}

	return report, nil
}

// ─── Chat ─────────────────────────────────────────────────────────────────────

// Chat answers a user question using a single ReAct iteration. The LLM may call
// tools once before composing its reply. simID is used for simulation context.
func (a *Agent) Chat(
	ctx context.Context,
	projectID, simID, question string,
	history []llm.Message,
) (string, error) {
	tools := a.buildTools(ctx, projectID, simID)

	const chatSystem = `You are a simulation analyst assistant with access to a knowledge graph.

AVAILABLE TOOLS (use XML tags to call a tool):
<tool_call>{"name": "insight_forge", "parameters": {"query": "your question"}}</tool_call>
<tool_call>{"name": "panorama_search", "parameters": {"query": "topic"}}</tool_call>
<tool_call>{"name": "quick_search", "parameters": {"query": "entity name", "limit": 10}}</tool_call>
<tool_call>{"name": "interview_agents", "parameters": {"topic": "question", "agent_names": []}}</tool_call>
<tool_call>{"name": "get_graph_statistics", "parameters": {}}</tool_call>
<tool_call>{"name": "get_entity_summary", "parameters": {"entity_name": "Alice"}}</tool_call>

You may call one or more tools to gather information, then answer the user's question.
If no tools are needed, answer directly.`

	messages := []llm.Message{{Role: "system", Content: chatSystem}}
	messages = append(messages, history...)
	messages = append(messages, llm.Message{Role: "user", Content: question})

	// First pass: may contain tool calls.
	response, err := a.llm.Chat(ctx, messages)
	if err != nil {
		return "", err
	}

	calls := parseToolCalls(response)
	if len(calls) == 0 {
		return stripToolCalls(response), nil
	}

	// Execute tools and gather results.
	messages = append(messages, llm.Message{Role: "assistant", Content: response})

	var toolResults strings.Builder
	for _, tc := range calls {
		t := toolByName(tools, tc.Name)
		var result string
		if t == nil {
			result = fmt.Sprintf("Unknown tool: %s", tc.Name)
		} else {
			result = t.Execute(ctx, tc.Parameters)
		}
		fmt.Fprintf(&toolResults, "<tool_result tool=%q>\n%s\n</tool_result>\n\n", tc.Name, result)
	}

	messages = append(messages, llm.Message{
		Role:    "user",
		Content: toolResults.String() + "\nNow answer the original question using the tool results above.",
	})

	final, err := a.llm.Chat(ctx, messages)
	if err != nil {
		return "", err
	}
	return stripToolCalls(final), nil
}

// ─── Interview ────────────────────────────────────────────────────────────────

// Interview generates an in-character response from a graph node persona.
func (a *Agent) Interview(
	ctx context.Context,
	projectID, agentRef, question string,
	history []llm.Message,
) (string, llm.Message, error) {
	nodes, err := a.db.GetNodes(projectID)
	if err != nil {
		return "", llm.Message{}, err
	}

	// Find node by ID or name (case-insensitive).
	var node *db.Node
	refLow := strings.ToLower(agentRef)
	for _, n := range nodes {
		nn := n
		if n.ID == agentRef || strings.ToLower(n.Name) == refLow {
			node = &nn
			break
		}
	}
	if node == nil {
		return "", llm.Message{}, fmt.Errorf("agent %q not found in graph", agentRef)
	}

	system := fmt.Sprintf(
		"You are %s, a %s. %s\n\nSpeak as this persona. Be authentic, specific, and stay in character. Keep answers to 2-4 sentences.",
		node.Name, node.Type, node.Summary)

	msgs := []llm.Message{{Role: "system", Content: system}}
	msgs = append(msgs, history...)
	msgs = append(msgs, llm.Message{Role: "user", Content: question})

	resp, err := a.llm.Chat(ctx, msgs)
	if err != nil {
		return "", llm.Message{}, err
	}
	return resp, llm.Message{Role: "assistant", Content: resp}, nil
}

// ListAgents returns all nodes available for interview.
func (a *Agent) ListAgents(projectID string) ([]db.Node, error) {
	return a.db.GetNodes(projectID)
}

// InterviewBatch interviews multiple agents concurrently and returns a map of
// agentName -> response. Names are matched case-insensitively against graph node
// names. A semaphore limits concurrency to 4 simultaneous LLM calls.
// Results are returned for every requested name; if a name is not found or the
// interview fails, the map value describes the error.
func (a *Agent) InterviewBatch(
	ctx context.Context,
	projectID string,
	agentNames []string,
	question string,
) (map[string]string, error) {
	if len(agentNames) == 0 {
		return map[string]string{}, nil
	}

	nodes, err := a.db.GetNodes(projectID)
	if err != nil {
		return nil, fmt.Errorf("InterviewBatch: load nodes: %w", err)
	}

	// Build a case-insensitive name→node index.
	nodeByName := make(map[string]db.Node, len(nodes))
	for _, n := range nodes {
		nodeByName[strings.ToLower(n.Name)] = n
	}

	type entry struct {
		name string
		resp string
	}

	results := make([]entry, len(agentNames))
	for i, name := range agentNames {
		results[i].name = name
	}

	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup

	for i, name := range agentNames {
		wg.Add(1)
		go func(idx int, agentName string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			node, ok := nodeByName[strings.ToLower(agentName)]
			if !ok {
				results[idx].resp = fmt.Sprintf("[agent %q not found in graph]", agentName)
				return
			}
			resp, _, err := a.Interview(ctx, projectID, node.Name, question, nil)
			if err != nil {
				results[idx].resp = fmt.Sprintf("[interview error: %v]", err)
			} else {
				results[idx].resp = resp
			}
		}(i, name)
	}
	wg.Wait()

	out := make(map[string]string, len(results))
	for _, r := range results {
		out[r.name] = r.resp
	}
	return out, nil
}

// ─── Structured Interview ─────────────────────────────────────────────────────

// selectAgentsLLM uses LLM to pick the most relevant agent names for an interview topic.
// Returns up to maxN agent names from the available nodes. Falls back to the first maxN
// agents if the LLM call fails or returns no valid names.
func (a *Agent) selectAgentsLLM(ctx context.Context, projectID string, topic string, maxN int) ([]string, error) {
	nodes, err := a.db.GetNodes(projectID)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, nil
	}

	// Build a compact node list for LLM context.
	type nodeInfo struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Summary string `json:"summary"`
	}
	nodeList := make([]nodeInfo, 0, len(nodes))
	for _, n := range nodes {
		nodeList = append(nodeList, nodeInfo{
			Name:    n.Name,
			Type:    n.Type,
			Summary: truncate(n.Summary, 100),
		})
	}
	nodesJSON, _ := json.Marshal(nodeList)

	type selectionResult struct {
		Selected []string `json:"selected"`
		Reason   string   `json:"reason"`
	}

	prompt := fmt.Sprintf(
		"Interview topic: %q\n\nAgents:\n%s\n\nSelect the %d most relevant agents for this interview topic. Choose agents whose role, type, or expertise makes them likely to have meaningful perspectives on the topic.",
		topic, string(nodesJSON), maxN,
	)

	var result selectionResult
	err = a.llm.JSON(ctx,
		fmt.Sprintf("You are an interview director. Select up to %d agents most relevant to the interview topic. Return JSON: {\"selected\": [\"AgentName1\", ...], \"reason\": \"brief explanation\"}", maxN),
		prompt,
		&result,
	)
	firstN := func() []string {
		out := make([]string, 0, maxN)
		for i, n := range nodes {
			if i >= maxN {
				break
			}
			out = append(out, n.Name)
		}
		return out
	}

	if err != nil || len(result.Selected) == 0 {
		return firstN(), nil
	}

	// Validate returned names against actual node names (case-insensitive).
	nodeByName := make(map[string]string, len(nodes))
	for _, n := range nodes {
		nodeByName[strings.ToLower(n.Name)] = n.Name
	}
	var valid []string
	for _, name := range result.Selected {
		if canonical, ok := nodeByName[strings.ToLower(name)]; ok {
			valid = append(valid, canonical)
		}
		if len(valid) >= maxN {
			break
		}
	}
	if len(valid) == 0 {
		return firstN(), nil
	}
	return valid, nil
}

// SelectAgents uses the LLM to choose the most relevant agent names from the
// available pool given the question context. Returns up to maxN agent names.
// If the LLM fails or returns invalid names, falls back to the first maxN agents.
func SelectAgents(ctx context.Context, client *llm.Client, question string, availableAgents []string, maxN int) ([]string, error) {
	if len(availableAgents) == 0 {
		return nil, nil
	}
	if maxN <= 0 {
		maxN = len(availableAgents)
	}
	if maxN >= len(availableAgents) {
		return availableAgents, nil
	}

	agentList := strings.Join(availableAgents, "\n- ")
	prompt := fmt.Sprintf(
		"Given this question: %s\nFrom these agents:\n- %s\nSelect the %d most relevant agents to interview. Return a JSON array of agent names only.",
		question, agentList, maxN,
	)

	var selected []string
	err := client.JSON(ctx,
		"You are a selector that picks the most relevant agents to answer a question. Return only a JSON array of strings.",
		prompt,
		&selected,
	)
	if err != nil {
		// Graceful fallback: use first maxN agents.
		return availableAgents[:maxN], nil
	}

	// Validate that returned names exist in the available set.
	available := make(map[string]string, len(availableAgents))
	for _, a := range availableAgents {
		available[strings.ToLower(a)] = a
	}
	var valid []string
	for _, name := range selected {
		if canonical, ok := available[strings.ToLower(name)]; ok {
			valid = append(valid, canonical)
		}
		if len(valid) >= maxN {
			break
		}
	}
	if len(valid) == 0 {
		// Fallback: none of the LLM-selected names were valid.
		return availableAgents[:maxN], nil
	}
	return valid, nil
}

// extractKeyQuotes calls the LLM to pull 2-3 verbatim short phrases from a
// response that directly address the question. On failure returns nil gracefully.
func extractKeyQuotes(ctx context.Context, client *llm.Client, response, question string) []string {
	if response == "" {
		return nil
	}
	prompt := fmt.Sprintf(
		"Question: %s\n\nResponse: %s\n\nExtract 2-3 verbatim quotes (short phrases) from the response that directly address the question. Return a JSON array of strings.",
		question, response,
	)
	var quotes []string
	if err := client.JSON(ctx,
		"You are a quote extractor. Return only a JSON array of short verbatim strings from the given response.",
		prompt,
		&quotes,
	); err != nil {
		return nil
	}
	return quotes
}

// InterviewStructured runs a structured interview session: selects relevant
// agents, interviews them, extracts key quotes, and generates a synthesis summary.
func InterviewStructured(
	ctx context.Context,
	database *db.DB,
	client *llm.Client,
	projectID string,
	question string,
	maxAgents int,
) (*InterviewReport, error) {
	if maxAgents <= 0 {
		maxAgents = 3
	}

	// Step 1: get all node names.
	nodes, err := database.GetNodes(projectID)
	if err != nil {
		return nil, fmt.Errorf("InterviewStructured: load nodes: %w", err)
	}
	allNames := make([]string, 0, len(nodes))
	for _, n := range nodes {
		allNames = append(allNames, n.Name)
	}

	// Step 2: select relevant agents.
	selectedNames, err := SelectAgents(ctx, client, question, allNames, maxAgents)
	if err != nil {
		return nil, fmt.Errorf("InterviewStructured: select agents: %w", err)
	}
	if len(selectedNames) == 0 {
		return &InterviewReport{Question: question}, nil
	}

	// Step 3: run batch interviews — reuse the Agent's InterviewBatch via a local agent.
	a := &Agent{db: database, llm: client}
	rawResponses, err := a.InterviewBatch(ctx, projectID, selectedNames, question)
	if err != nil {
		return nil, fmt.Errorf("InterviewStructured: interview batch: %w", err)
	}

	// Step 4: extract key quotes for each response and build InterviewResult list.
	results := make([]InterviewResult, 0, len(selectedNames))
	var allResponsesBuilder strings.Builder
	for _, name := range selectedNames {
		resp := rawResponses[name]
		quotes := extractKeyQuotes(ctx, client, resp, question)
		results = append(results, InterviewResult{
			AgentName: name,
			Response:  resp,
			KeyQuotes: quotes,
		})
		fmt.Fprintf(&allResponsesBuilder, "%s: %s\n\n", name, resp)
	}

	// Step 5: generate synthesis summary.
	allResponses := strings.TrimSpace(allResponsesBuilder.String())
	synthesisPrompt := fmt.Sprintf(
		"Synthesize these agent perspectives on \"%s\" into a 2-3 sentence summary:\n\n%s",
		question, allResponses,
	)
	summary, _ := client.System(ctx,
		"You are a synthesis writer. Summarize multiple agent perspectives concisely.",
		synthesisPrompt,
	)

	return &InterviewReport{
		Question: question,
		Results:  results,
		Summary:  summary,
	}, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// formatSearchResult converts a graph.SearchResult into a readable string.
func formatSearchResult(r graph.SearchResult) string {
	var sb strings.Builder
	if len(r.Nodes) == 0 && len(r.Facts) == 0 {
		return fmt.Sprintf("No results found for query: %q", r.Query)
	}
	if len(r.Nodes) > 0 {
		fmt.Fprintf(&sb, "Nodes (%d):\n", len(r.Nodes))
		for _, n := range r.Nodes {
			fmt.Fprintf(&sb, "  - %s (%s): %s\n", n.Name, n.Type, truncate(n.Summary, 200))
		}
	}
	if len(r.Facts) > 0 {
		fmt.Fprintf(&sb, "\nFacts (%d):\n", len(r.Facts))
		for _, f := range r.Facts {
			fmt.Fprintf(&sb, "  - %s\n", f)
		}
	}
	return strings.TrimSpace(sb.String())
}

// truncate shortens s to at most maxLen characters, appending "…" if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// now returns the current UTC time as an RFC3339 string.
func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// sanitizeID replaces characters unsuitable for filenames with underscores.
var sanitizeRE = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func sanitizeID(s string) string {
	s = sanitizeRE.ReplaceAllString(s, "_")
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// writeReportFile writes markdown content to path (best-effort, no error returned).
func writeReportFile(path, content string) {
	_ = os.WriteFile(path, []byte(content), 0644)
}
