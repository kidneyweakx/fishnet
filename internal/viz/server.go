package viz

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"fishnet/internal/db"
	"fishnet/internal/llm"
	"fishnet/internal/platform"
	"fishnet/internal/sim"
)

// ─── Graph JSON format for D3 ─────────────────────────────────────────────────

type vizNode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Summary     string `json:"summary"`
	CommunityID int    `json:"community"`
}

type agentCard struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	NodeType        string   `json:"node_type"`
	Summary         string   `json:"summary"`
	CommunityID     int      `json:"community_id"`
	HasPersonality  bool     `json:"has_personality"` // true once GenerateSimConfig has run
	// Behavioral
	Stance          string   `json:"stance"`
	ActivityLevel   float64  `json:"activity_level"`
	SentimentBias   float64  `json:"sentiment_bias"`
	InfluenceWeight float64  `json:"influence_weight"`
	Reactivity      float64  `json:"reactivity"`
	Originality     float64  `json:"originality"`
	PostsPerHour    float64  `json:"posts_per_hour"`
	CommentsPerHour float64  `json:"comments_per_hour"`
	ActiveHours     []int    `json:"active_hours"`
	// Big Five
	Creativity      float64  `json:"creativity"`
	Rationality     float64  `json:"rationality"`
	Empathy         float64  `json:"empathy"`
	Extraversion    float64  `json:"extraversion"`
	Openness        float64  `json:"openness"`
	// Rich persona
	Username        string   `json:"username"`
	Profession      string   `json:"profession"`
	Location        string   `json:"location"`
	Timezone        string   `json:"timezone"`
	Interests       []string `json:"interests"`
	Catchphrases    []string `json:"catchphrases"`
}

type vizEdge struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Type   string  `json:"type"`
	Fact   string  `json:"fact"`
	Weight float64 `json:"weight"`
}

type vizGraph struct {
	Nodes []vizNode `json:"nodes"`
	Edges []vizEdge `json:"edges"`
}

// ─── Simulation state ─────────────────────────────────────────────────────────

// simEvent is one SSE event sent to the browser during a simulation.
type simEvent struct {
	Type     string   `json:"type"`               // "action"|"round"|"log"|"done"|"error"
	Round    int      `json:"round,omitempty"`
	MaxRound int      `json:"max_rounds,omitempty"`
	TWPosts  int      `json:"tw_posts,omitempty"`
	RDPosts  int      `json:"rd_posts,omitempty"`
	Log      string   `json:"log,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// simWebResult is the JSON returned by /api/sim/result.
type simWebResult struct {
	SimMetrics *sim.SimMetrics `json:"sim_metrics"`
	TopQuotes  []simWebQuote   `json:"top_quotes"`
}

type simWebQuote struct {
	Agent     string `json:"agent"`
	Platform  string `json:"platform"`
	Content   string `json:"content"`
	Round     int    `json:"round"`
	Reactions int    `json:"reactions"`
}

// vizServer holds HTTP server state including live simulation tracking.
type vizServer struct {
	db        *db.DB
	projectID string
	client    *llm.Client // may be nil (NoLLM only)

	mu      sync.Mutex
	running bool
	events  []simEvent // append-only event log; SSE clients tail this
	result  []byte     // JSON-encoded simWebResult; nil until sim completes
	simErr  string

	// Generate personalities state
	genRunning bool
	genErr     string
}

func (vs *vizServer) appendEvent(ev simEvent) {
	vs.mu.Lock()
	vs.events = append(vs.events, ev)
	vs.mu.Unlock()
}

// ─── Serve ────────────────────────────────────────────────────────────────────

// Serve starts a local HTTP server and returns its URL.
// client may be nil; if nil, only ModeNoLLM simulations are allowed.
func Serve(database *db.DB, projectID string, client *llm.Client) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}

	vs := &vizServer{
		db:        database,
		projectID: projectID,
		client:    client,
	}

	mux := http.NewServeMux()

	// ── Existing pages ───────────────────────────────────────────────────────
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, htmlTemplate)
	})
	mux.HandleFunc("/step2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, step2Template)
	})

	// ── Simulation UI ────────────────────────────────────────────────────────
	mux.HandleFunc("/sim", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, simTemplate)
	})

	// ── Graph APIs ───────────────────────────────────────────────────────────
	mux.HandleFunc("/api/graph", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		g, err := buildVizGraph(database, projectID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(g)
	})

	// ── Agent APIs ───────────────────────────────────────────────────────────
	// Order matters: more specific paths first.
	mux.HandleFunc("/api/agents/generate", vs.handleAgentsGenerate) // POST: start, GET: status
	mux.HandleFunc("/api/agents/merge",    vs.handleAgentsMerge)    // POST: merge two
	mux.HandleFunc("/api/agents/",         vs.handleAgentByID)      // GET/PUT/DELETE /:id
	mux.HandleFunc("/api/agents",          vs.handleAgentsList)      // GET: list, POST: create

	// ── Simulation APIs ──────────────────────────────────────────────────────
	mux.HandleFunc("/api/sim/run",      vs.handleSimRun)
	mux.HandleFunc("/api/sim/progress", vs.handleSimProgress)
	mux.HandleFunc("/api/sim/status",   vs.handleSimStatus)
	mux.HandleFunc("/api/sim/result",   vs.handleSimResult)

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)

	return fmt.Sprintf("http://%s", ln.Addr()), nil
}

// ─── Simulation handlers ──────────────────────────────────────────────────────

type simRunRequest struct {
	Scenario  string   `json:"scenario"`
	Rounds    int      `json:"rounds"`
	Mode      string   `json:"mode"`
	Agents    int      `json:"agents"`
	Platforms []string `json:"platforms"`
}

func (vs *vizServer) handleSimRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req simRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Scenario = strings.TrimSpace(req.Scenario)
	if req.Scenario == "" {
		http.Error(w, "scenario is required", http.StatusBadRequest)
		return
	}
	if req.Rounds <= 0 {
		req.Rounds = 10
	}
	if len(req.Platforms) == 0 {
		req.Platforms = []string{"twitter", "reddit"}
	}
	if req.Mode == "" {
		req.Mode = sim.ModeBatch
	}
	// Enforce NoLLM when no client
	if vs.client == nil && req.Mode != sim.ModeNoLLM {
		req.Mode = sim.ModeNoLLM
	}

	vs.mu.Lock()
	if vs.running {
		vs.mu.Unlock()
		http.Error(w, "simulation already running", http.StatusConflict)
		return
	}
	vs.running = true
	vs.events = nil
	vs.result = nil
	vs.simErr = ""
	vs.mu.Unlock()

	go vs.runSim(r.Context(), req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "started", "mode": req.Mode})
}

func (vs *vizServer) runSim(parentCtx context.Context, req simRunRequest) {
	defer func() {
		vs.mu.Lock()
		vs.running = false
		vs.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = parentCtx // keep for future cancellation support

	ps := sim.NewPlatformSim(vs.db, vs.client)
	progressCh := make(chan sim.RoundProgress, 256)

	cfg := sim.RoundConfig{
		Scenario:    req.Scenario,
		MaxRounds:   req.Rounds,
		MaxAgents:   req.Agents,
		Platforms:   req.Platforms,
		Concurrency: 4,
		Mode:        req.Mode,
	}

	done := make(chan error, 1)
	go func() {
		done <- ps.Run(ctx, vs.projectID, cfg, progressCh)
	}()

	// Collect actions for post-sim analytics.
	// Each emit() in rounds.go sends exactly one RoundProgress with one Action.
	// There is no explicit "round boundary" event; we detect it via round-number change.
	var allActions []platform.Action
	lastRound := 0

	for prog := range progressCh {
		if prog.Done {
			// Final stats — emit last round event at 100%
			vs.appendEvent(simEvent{
				Type:     "round",
				Round:    prog.Round,
				MaxRound: prog.MaxRounds,
				TWPosts:  prog.TwitterStat.Posts,
				RDPosts:  prog.RedditStat.Posts,
			})
			break
		}
		if prog.Error != nil {
			vs.appendEvent(simEvent{Type: "error", Error: prog.Error.Error()})
			<-done
			return
		}

		// Detect round boundary: when the round number advances, emit a round event
		// so the progress bar updates in the browser.
		if prog.Round > lastRound {
			if lastRound > 0 {
				vs.appendEvent(simEvent{
					Type:     "round",
					Round:    lastRound,
					MaxRound: prog.MaxRounds,
					TWPosts:  prog.TwitterStat.Posts,
					RDPosts:  prog.RedditStat.Posts,
				})
			}
			lastRound = prog.Round
		}

		// One action per progress event
		if prog.Action.Type != "" {
			if prog.Action.Success {
				allActions = append(allActions, prog.Action)
			}
			vs.appendEvent(simEvent{
				Type:  "action",
				Round: prog.Round,
				Log:   fmt.Sprintf("[r%d] %s", prog.Round, prog.Action.Description()),
			})
		}

		// Forward non-fatal log messages (e.g. graph memory errors)
		for _, l := range prog.Logs {
			vs.appendEvent(simEvent{Type: "log", Log: l})
		}
	}

	if err := <-done; err != nil {
		vs.appendEvent(simEvent{Type: "error", Error: err.Error()})
		return
	}

	// ── Compute metrics + top quotes ─────────────────────────────────────────
	metrics := sim.ComputeMetrics(allActions, nil)
	quotes := buildWebQuotes(allActions, 5)

	result := simWebResult{SimMetrics: &metrics, TopQuotes: quotes}
	b, _ := json.Marshal(result)

	vs.mu.Lock()
	vs.result = b
	vs.mu.Unlock()

	vs.appendEvent(simEvent{Type: "done"})
}

// buildWebQuotes derives the top n most-reacted posts from a flat action log.
func buildWebQuotes(actions []platform.Action, n int) []simWebQuote {
	type postRec struct {
		quote     simWebQuote
		reactions int
	}
	idx := make(map[string]*postRec)

	for _, a := range actions {
		if a.Content != "" && a.PostID != "" {
			if _, ok := idx[a.PostID]; !ok {
				idx[a.PostID] = &postRec{quote: simWebQuote{
					Agent:    a.AgentName,
					Platform: a.Platform,
					Content:  a.Content,
					Round:    a.Round,
				}}
			}
		}
	}
	for _, a := range actions {
		rec, ok := idx[a.PostID]
		if !ok {
			continue
		}
		switch a.Type {
		case platform.ActLikePost, platform.ActLikeComment:
			rec.reactions++
		case platform.ActRepost:
			rec.reactions += 2
		}
	}

	posts := make([]simWebQuote, 0, len(idx))
	for _, rec := range idx {
		rec.quote.Reactions = rec.reactions
		posts = append(posts, rec.quote)
	}
	sort.Slice(posts, func(i, j int) bool { return posts[i].Reactions > posts[j].Reactions })
	if len(posts) > n {
		posts = posts[:n]
	}
	return posts
}

func (vs *vizServer) handleSimProgress(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	pos := 0
	for {
		select {
		case <-r.Context().Done():
			return
		default:
		}

		vs.mu.Lock()
		toSend := append([]simEvent{}, vs.events[pos:]...)
		pos = len(vs.events)
		running := vs.running
		vs.mu.Unlock()

		for _, ev := range toSend {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		if len(toSend) > 0 {
			flusher.Flush()
		}

		if !running && len(toSend) == 0 {
			return
		}

		time.Sleep(80 * time.Millisecond)
	}
}

func (vs *vizServer) handleSimStatus(w http.ResponseWriter, r *http.Request) {
	vs.mu.Lock()
	running := vs.running
	evCount := len(vs.events)
	hasResult := vs.result != nil
	vs.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"running":    running,
		"events":     evCount,
		"has_result": hasResult,
	})
}

func (vs *vizServer) handleSimResult(w http.ResponseWriter, r *http.Request) {
	vs.mu.Lock()
	b := vs.result
	vs.mu.Unlock()

	if b == nil {
		http.Error(w, "no result available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(b)
}

// ─── Graph helpers ────────────────────────────────────────────────────────────

func buildVizGraph(database *db.DB, projectID string) (*vizGraph, error) {
	nodes, err := database.GetNodes(projectID)
	if err != nil {
		return nil, err
	}
	edges, err := database.GetEdges(projectID)
	if err != nil {
		return nil, err
	}

	g := &vizGraph{}
	for _, n := range nodes {
		g.Nodes = append(g.Nodes, vizNode{
			ID:          n.ID,
			Name:        n.Name,
			Type:        n.Type,
			Summary:     n.Summary,
			CommunityID: n.CommunityID,
		})
	}
	for _, e := range edges {
		g.Edges = append(g.Edges, vizEdge{
			Source: e.SourceID,
			Target: e.TargetID,
			Type:   e.Type,
			Fact:   e.Fact,
			Weight: e.Weight,
		})
	}
	return g, nil
}

// PrintASCII prints a simple ASCII summary of the graph to stdout.
func PrintASCII(database *db.DB, projectID string) error {
	nodes, err := database.GetNodes(projectID)
	if err != nil {
		return err
	}
	edges, err := database.GetEdges(projectID)
	if err != nil {
		return err
	}

	byType := make(map[string][]db.Node)
	for _, n := range nodes {
		byType[n.Type] = append(byType[n.Type], n)
	}

	fmt.Printf("\n\033[1mGraph Overview\033[0m (%d nodes, %d edges)\n", len(nodes), len(edges))
	fmt.Println(strings.Repeat("─", 50))

	for typ, ns := range byType {
		fmt.Printf("\n\033[36m%s\033[0m (%d)\n", typ, len(ns))
		for i, n := range ns {
			if i >= 5 {
				fmt.Printf("  ... and %d more\n", len(ns)-i)
				break
			}
			summary := n.Summary
			if len(summary) > 60 {
				summary = summary[:60] + "..."
			}
			comm := ""
			if n.CommunityID >= 0 {
				comm = fmt.Sprintf(" [c%d]", n.CommunityID)
			}
			fmt.Printf("  • %s%s: %s\n", n.Name, comm, summary)
		}
	}

	fmt.Printf("\n\033[36mRelationships\033[0m (showing up to 10)\n")
	nodeByID := make(map[string]db.Node)
	for _, n := range nodes {
		nodeByID[n.ID] = n
	}
	for i, e := range edges {
		if i >= 10 {
			fmt.Printf("  ... and %d more\n", len(edges)-i)
			break
		}
		src := nodeByID[e.SourceID].Name
		tgt := nodeByID[e.TargetID].Name
		fmt.Printf("  %s -[%s]-> %s\n", src, e.Type, tgt)
	}
	fmt.Println()
	return nil
}

// ─── Agent helpers ────────────────────────────────────────────────────────────

// nodeToAgentCard converts a db.Node into an agentCard.
// It reads persisted personality fields from node.Attributes when available,
// and falls back to deterministic FromNode defaults otherwise.
func nodeToAgentCard(n db.Node, idx int) agentCard {
	p := platform.FromNode(n, idx) // seeded-random defaults

	card := agentCard{
		ID:              n.ID,
		Name:            n.Name,
		NodeType:        n.Type,
		Summary:         n.Summary,
		CommunityID:     n.CommunityID,
		// defaults from FromNode
		Stance:          p.Stance,
		ActivityLevel:   p.ActivityLevel,
		SentimentBias:   p.SentimentBias,
		InfluenceWeight: p.InfluenceWeight,
		Reactivity:      p.Reactivity,
		Originality:     p.Originality,
		PostsPerHour:    p.PostsPerHour,
		CommentsPerHour: p.CommentsPerHour,
		ActiveHours:     p.ActiveHours,
		Creativity:      p.Creativity,
		Rationality:     p.Rationality,
		Empathy:         p.Empathy,
		Extraversion:    p.Extraversion,
		Openness:        p.Openness,
		Interests:       p.Interests,
	}

	if n.Attributes == "" || n.Attributes == "{}" {
		return card
	}

	var attrs map[string]json.RawMessage
	if err := json.Unmarshal([]byte(n.Attributes), &attrs); err != nil {
		return card
	}

	getFloat := func(key string) (float64, bool) {
		raw, ok := attrs[key]
		if !ok {
			return 0, false
		}
		var v float64
		if json.Unmarshal(raw, &v) == nil {
			return v, true
		}
		return 0, false
	}
	getString := func(key string) (string, bool) {
		raw, ok := attrs[key]
		if !ok {
			return "", false
		}
		var v string
		if json.Unmarshal(raw, &v) == nil {
			return v, true
		}
		return "", false
	}
	getStrings := func(key string) []string {
		raw, ok := attrs[key]
		if !ok {
			return nil
		}
		var v []string
		if json.Unmarshal(raw, &v) == nil {
			return v
		}
		return nil
	}
	getBool := func(key string) bool {
		raw, ok := attrs[key]
		if !ok {
			return false
		}
		var v bool
		return json.Unmarshal(raw, &v) == nil && v
	}

	card.HasPersonality = getBool("has_personality")

	if v, ok := getString("stance"); ok && v != "" {
		card.Stance = v
	}
	if v, ok := getFloat("activity_level"); ok {
		card.ActivityLevel = v
	}
	if v, ok := getFloat("sentiment_bias"); ok {
		card.SentimentBias = v
	}
	if v, ok := getFloat("influence_weight"); ok {
		card.InfluenceWeight = v
	}
	if v, ok := getFloat("reactivity"); ok {
		card.Reactivity = v
	}
	if v, ok := getFloat("originality"); ok {
		card.Originality = v
	}
	if v, ok := getFloat("posts_per_hour"); ok {
		card.PostsPerHour = v
	}
	if v, ok := getFloat("comments_per_hour"); ok {
		card.CommentsPerHour = v
	}
	if v, ok := getFloat("creativity"); ok {
		card.Creativity = v
	}
	if v, ok := getFloat("rationality"); ok {
		card.Rationality = v
	}
	if v, ok := getFloat("empathy"); ok {
		card.Empathy = v
	}
	if v, ok := getFloat("extraversion"); ok {
		card.Extraversion = v
	}
	if v, ok := getFloat("openness"); ok {
		card.Openness = v
	}
	if v, ok := getString("username"); ok {
		card.Username = v
	}
	if v, ok := getString("profession"); ok {
		card.Profession = v
	}
	if v, ok := getString("location"); ok {
		card.Location = v
	}
	if v, ok := getString("timezone"); ok {
		card.Timezone = v
	}
	if v := getStrings("interests"); v != nil {
		card.Interests = v
	}
	if v := getStrings("catchphrases"); v != nil {
		card.Catchphrases = v
	}

	return card
}

// jsonOK writes v as JSON with 200 OK.
func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(v)
}

// ─── Agent list & create ──────────────────────────────────────────────────────

func (vs *vizServer) handleAgentsList(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		nodes, err := vs.db.GetNodes(vs.projectID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		agents := make([]agentCard, len(nodes))
		for i, n := range nodes {
			agents[i] = nodeToAgentCard(n, i)
		}
		jsonOK(w, agents)

	case http.MethodPost:
		// Create a new custom agent.
		var req struct {
			Name     string `json:"name"`
			NodeType string `json:"node_type"`
			Summary  string `json:"summary"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
			http.Error(w, "name required", http.StatusBadRequest)
			return
		}
		if req.NodeType == "" {
			req.NodeType = "Person"
		}
		id, err := vs.db.InsertNode(vs.projectID, req.Name, req.NodeType, req.Summary, `{"extracted":"false"}`)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		n, _ := vs.db.GetNode(id)
		jsonOK(w, nodeToAgentCard(n, 0))

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Agent by ID (GET / PUT / DELETE) ────────────────────────────────────────

func (vs *vizServer) handleAgentByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	if id == "" {
		http.Error(w, "missing agent id", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		n, err := vs.db.GetNode(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		jsonOK(w, nodeToAgentCard(n, 0))

	case http.MethodPut:
		// Update agent personality fields.
		n, err := vs.db.GetNode(id)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// Decode request body as a map to patch into existing attrs.
		var patch map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Merge patch into existing attributes.
		existing := make(map[string]json.RawMessage)
		if n.Attributes != "" && n.Attributes != "{}" {
			json.Unmarshal([]byte(n.Attributes), &existing)
		}
		for k, v := range patch {
			existing[k] = v
		}
		// Mark as having a personality if personality fields are present.
		existing["has_personality"] = json.RawMessage(`true`)

		raw, _ := json.Marshal(existing)
		if err := vs.db.UpdateNodeAttributes(id, string(raw)); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		n, _ = vs.db.GetNode(id)
		jsonOK(w, nodeToAgentCard(n, 0))

	case http.MethodDelete:
		if err := vs.db.DeleteNode(id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		jsonOK(w, map[string]string{"deleted": id})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ─── Merge ────────────────────────────────────────────────────────────────────

func (vs *vizServer) handleAgentsMerge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		KeepID string `json:"keep_id"`
		DropID string `json:"drop_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.KeepID == "" || req.DropID == "" {
		http.Error(w, "keep_id and drop_id required", http.StatusBadRequest)
		return
	}
	if err := vs.db.MergeNodes(req.KeepID, req.DropID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	n, _ := vs.db.GetNode(req.KeepID)
	jsonOK(w, nodeToAgentCard(n, 0))
}

// ─── Generate personalities ───────────────────────────────────────────────────

func (vs *vizServer) handleAgentsGenerate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		vs.mu.Lock()
		running := vs.genRunning
		genErr := vs.genErr
		vs.mu.Unlock()
		jsonOK(w, map[string]interface{}{"running": running, "error": genErr})

	case http.MethodPost:
		if vs.client == nil {
			http.Error(w, "no LLM client configured (start fishnet with API key)", http.StatusServiceUnavailable)
			return
		}
		var req struct {
			Scenario string `json:"scenario"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if strings.TrimSpace(req.Scenario) == "" {
			req.Scenario = "general social media discussion"
		}

		vs.mu.Lock()
		if vs.genRunning {
			vs.mu.Unlock()
			http.Error(w, "generation already running", http.StatusConflict)
			return
		}
		vs.genRunning = true
		vs.genErr = ""
		vs.mu.Unlock()

		go vs.runGenerate(req.Scenario)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "started"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (vs *vizServer) runGenerate(scenario string) {
	defer func() {
		vs.mu.Lock()
		vs.genRunning = false
		vs.mu.Unlock()
	}()

	ctx := context.Background()

	nodes, err := vs.db.GetNodes(vs.projectID)
	if err != nil || len(nodes) == 0 {
		vs.mu.Lock()
		vs.genErr = "no nodes found"
		vs.mu.Unlock()
		return
	}

	cfg, err := platform.GenerateSimConfig(ctx, vs.client, nodes, scenario, "UTC", 4)
	if err != nil {
		vs.mu.Lock()
		vs.genErr = err.Error()
		vs.mu.Unlock()
		return
	}

	// Build personalities and apply config, then persist to DB.
	personalities := make([]*platform.Personality, len(nodes))
	for i, n := range nodes {
		personalities[i] = platform.FromNode(n, i)
	}
	platform.ApplySimConfig(personalities, cfg)
	platform.PersistPersonalityAttrs(vs.db, personalities)
}
