// Package tui implements the interactive terminal UI for fishnet.
// Mirrors MiroFish's 5-step flow: Graph → Entities → Simulate → Report → Interview.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"fishnet/internal/config"
	"fishnet/internal/db"
	"fishnet/internal/doc"
	"fishnet/internal/graph"
	"fishnet/internal/llm"
	"fishnet/internal/platform"
	"fishnet/internal/report"
	"fishnet/internal/session"
	"fishnet/internal/sim"
	"fishnet/internal/viz"
)

// ─── Screens (tabs) ───────────────────────────────────────────────────────────

type screen int

const (
	screenWizard  screen = -1 // Setup wizard (no project yet)
	screenDash    screen = 0  // Overview dashboard
	screenGraph   screen = 1  // Step 1: Build/view graph
	screenEnv     screen = 2  // Step 2: Entity browser
	screenSim     screen = 3  // Step 3: Simulation
	screenReport  screen = 4  // Step 4: Report generation
	screenChat    screen = 5  // Step 5: Agent interview
	screenHelp    screen = 6  // Help overlay
	screenSession screen = 7  // Session manager
	screenQuery   screen = 8  // Query / history browser
	screenDebug   screen = 9  // Debug info overlay
)

var tabLabels = []string{
	"[1] Graph",
	"[2] Entities",
	"[3] Simulate",
	"[4] Report",
	"[5] Interview",
	"[s] Sessions",
	"[q] History",
}

// focusPanel indicates which panel has keyboard focus in split-panel views.
type focusPanel int

const (
	focusLeft  focusPanel = iota
	focusRight focusPanel = iota
)

// ─── Async messages ───────────────────────────────────────────────────────────

type analyzeStartMsg struct{ dir string }
type analyzeProgressMsg struct {
	done, total int64
	nodes, edges int64
	err          error
	final        bool
}
type simProgressMsg struct {
	prog sim.RoundProgress
}
type simDoneMsg struct{ err error }
type reportSectionMsg struct {
	section report.Section
	done    bool
	err     error
}
type interviewRespMsg struct {
	resp string
	err  error
}
type logMsg struct{ text string }

// wizardProgressMsg carries incremental wizard stage feedback.
type wizardProgressMsg struct{ stage string }

// proxyStatusMsg is sent by checkProxyCmd when the CLIProxyAPI health check completes.
type proxyStatusMsg struct{ ok bool }

// checkProxyCmd runs a background health check against the CLIProxyAPI server.
func checkProxyCmd(baseURL string) tea.Cmd {
	return func() tea.Msg {
		ok := llm.CheckProxy(baseURL)
		return proxyStatusMsg{ok: ok}
	}
}

// llmStatusMsg is sent by checkLLMStatusCmd when the LLM provider status check completes.
type llmStatusMsg struct {
	ok  bool
	msg string // e.g. "claude-code: ready" or "openai: key set" or "codex: binary not found"
}

// checkLLMStatusCmd runs a background check appropriate for the configured LLM provider.
func checkLLMStatusCmd(cfg *config.Config) tea.Cmd {
	return func() tea.Msg {
		provider := cfg.LLM.Provider
		switch provider {
		case "claude-code":
			ok := llm.CheckClaudeCode()
			msg := "claude: not found"
			if ok {
				msg = "claude: ready"
			}
			return llmStatusMsg{ok: ok, msg: msg}
		case "codex-cli":
			ok := llm.CheckCodexCLI()
			msg := "codex: not found"
			if ok {
				msg = "codex: ready"
			}
			return llmStatusMsg{ok: ok, msg: msg}
		case "clicliproxy":
			baseURL := cfg.LLM.BaseURL
			if baseURL == "" {
				baseURL = "http://localhost:8080/v1"
			}
			ok := llm.CheckProxy(baseURL)
			msg := "proxy: offline"
			if ok {
				msg = "proxy: ok"
			}
			return llmStatusMsg{ok: ok, msg: msg}
		case "ollama":
			return llmStatusMsg{ok: true, msg: "ollama: local"}
		default: // openai, anthropic, codex, custom
			ok := llm.CheckAPIKey(provider, cfg.LLM.APIKey)
			msg := provider + ": no key"
			if ok {
				msg = provider + ": key set"
			}
			return llmStatusMsg{ok: ok, msg: msg}
		}
	}
}

// ─── App model ────────────────────────────────────────────────────────────────

type App struct {
	// Core
	screen    screen
	prevScreen screen
	width     int
	height    int
	cfg       *config.Config
	db        *db.DB
	projectID string

	// Shared components
	spinner  spinner.Model
	viewport viewport.Model

	// Panel focus (for split views)
	panelFocus focusPanel

	// Step 1: Graph build
	analyzeDir     string
	analyzeRunning bool
	analyzeProg    analyzeProgressMsg
	graphNodes     []db.Node
	graphEdges     []db.Edge

	// Step 2: Entity browser
	entityIdx    int
	entitySearch string
	entitySearchMode bool

	// Step 3: Simulation
	simInput      textinput.Model
	simScenario   string
	simRounds     int
	simMaxAgents  int
	simPlatforms  []string
	simRunning    bool
	simDone       bool
	simBackground bool // run without blocking navigation
	simRound      int
	simMaxRounds  int
	simActions    []platform.Action // rolling last 80
	simTwActions  []platform.Action // rolling last 40 twitter
	simRdActions  []platform.Action // rolling last 40 reddit
	simTwStat     platform.Stats
	simRdStat     platform.Stats
	simCh         chan sim.RoundProgress
	simStartTime  time.Time

	// Step 4: Report
	reportRunning    bool
	reportSections   []report.Section
	reportContent    string
	reportSectionIdx int // selected section in left panel
	reportTools      []string // tool call log during report generation

	// Step 5: Interview
	chatInput   textinput.Model
	chatTarget  string // agent name/ID
	chatHistory []llm.Message
	chatLines   []string // rendered chat for display

	// Shared log
	logs    []string
	maxLogs int

	// Wizard (shown when projectID == "")
	wizardStep int // 0=name input, 1=dir input, 2=running
	wizardName textinput.Model
	wizardDir  textinput.Model
	wizardMsg  string
	wizardCh   chan tea.Msg // progress channel while wizard is running

	// Session screen
	sessionList []*session.Session
	sessionIdx  int

	// Query/History screen
	queryTab     int // 0=posts, 1=actions, 2=timeline, 3=stats
	queryContent string
	queryLines   []string
	queryIdx     int

	// CLIProxyAPI proxy status
	proxyOK    bool
	proxyCheck time.Time // last check time

	// LLM status indicator (covers all providers)
	llmStatusOK  bool
	llmStatusMsg string

	// Debug screen
	debugLines []string
}

// ─── Run ─────────────────────────────────────────────────────────────────────

func Run(cfg *config.Config, database *db.DB, projectID string) error {
	m := newApp(cfg, database, projectID)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

func newApp(cfg *config.Config, database *db.DB, projectID string) App {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(colBlue))

	vp := viewport.New(80, 20)

	si := textinput.New()
	si.Placeholder = "Describe your scenario…"
	si.CharLimit = 300
	si.Width = 70

	ci := textinput.New()
	ci.Placeholder = "Ask the agent a question…"
	ci.CharLimit = 300
	ci.Width = 70

	// Wizard inputs
	wn := textinput.New()
	wn.Placeholder = "my-project"
	wn.CharLimit = 80
	wn.Width = 40

	wd := textinput.New()
	wd.Placeholder = "./docs"
	wd.CharLimit = 200
	wd.Width = 40

	startScreen := screenDash
	if projectID == "" {
		startScreen = screenWizard
		wn.Focus()
	}

	m := App{
		cfg:          cfg,
		db:           database,
		projectID:    projectID,
		screen:       startScreen,
		spinner:      sp,
		viewport:     vp,
		simInput:     si,
		chatInput:    ci,
		simRounds:    10,
		simMaxAgents: 25,
		simPlatforms: []string{"twitter", "reddit"},
		analyzeDir:   ".",
		maxLogs:      80,
		panelFocus:   focusLeft,
		wizardName:   wn,
		wizardDir:    wd,
	}

	if projectID != "" {
		nodes, _ := database.GetNodes(projectID)
		edges, _ := database.GetEdges(projectID)
		m.graphNodes = nodes
		m.graphEdges = edges
		if len(nodes) > 0 {
			m.chatTarget = nodes[0].Name
		}
	}

	return m
}

// ─── Init ─────────────────────────────────────────────────────────────────────

func (m App) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick, tickEvery(100 * time.Millisecond)}
	cmds = append(cmds, checkLLMStatusCmd(m.cfg))
	return tea.Batch(cmds...)
}

// ─── Update ───────────────────────────────────────────────────────────────────

func (m App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport.Width = m.width - 4
		m.viewport.Height = m.height - 8

	case tea.KeyMsg:
		// For the wizard screen, update the active text input BEFORE handleKey
		// so that handleWizardKey reads up-to-date values (e.g. when Enter is pressed).
		wizardInputHandled := false
		if m.screen == screenWizard {
			switch m.wizardStep {
			case 0:
				var tiCmd tea.Cmd
				m.wizardName, tiCmd = m.wizardName.Update(msg)
				if tiCmd != nil {
					cmds = append(cmds, tiCmd)
				}
				wizardInputHandled = true
			case 1:
				var tiCmd tea.Cmd
				m.wizardDir, tiCmd = m.wizardDir.Update(msg)
				if tiCmd != nil {
					cmds = append(cmds, tiCmd)
				}
				wizardInputHandled = true
			}
		}
		cmd := m.handleKey(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		_ = wizardInputHandled // used below to skip double-processing

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft {
			cmd := m.handleMouseClick(msg.X, msg.Y)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	case time.Time:
		// Drain sim channel and wizard progress channel
		cmds = append(cmds, m.drainSimCh(), m.drainWizardCh())

	case analyzeProgressMsg:
		m.analyzeProg = msg
		if msg.final || msg.err != nil {
			m.analyzeRunning = false
			if msg.err != nil {
				m.addLog("✗ Analyze error: " + msg.err.Error())
				m.addLog("✗ LLM error: " + msg.err.Error())
			} else {
				m.addLog(fmt.Sprintf("✓ Done: +%d nodes, +%d edges", msg.nodes, msg.edges))
				nodes, _ := m.db.GetNodes(m.projectID)
				edges, _ := m.db.GetEdges(m.projectID)
				m.graphNodes = nodes
				m.graphEdges = edges
			}
		}

	case simProgressMsg:
		prog := msg.prog
		m.simRound = prog.Round
		m.simMaxRounds = prog.MaxRounds
		m.simTwStat = prog.TwitterStat
		m.simRdStat = prog.RedditStat
		if prog.Action.AgentName != "" {
			m.simActions = append(m.simActions, prog.Action)
			if len(m.simActions) > 80 {
				m.simActions = m.simActions[len(m.simActions)-80:]
			}
			// Populate split platform feeds
			if prog.Action.Platform == "twitter" {
				m.simTwActions = append(m.simTwActions, prog.Action)
				if len(m.simTwActions) > 40 {
					m.simTwActions = m.simTwActions[len(m.simTwActions)-40:]
				}
			} else if prog.Action.Platform == "reddit" {
				m.simRdActions = append(m.simRdActions, prog.Action)
				if len(m.simRdActions) > 40 {
					m.simRdActions = m.simRdActions[len(m.simRdActions)-40:]
				}
			}
		}

	case simDoneMsg:
		m.simRunning = false
		m.simDone = true
		m.simBackground = false
		m.simCh = nil
		if msg.err != nil {
			m.addLog("✗ Sim error: " + msg.err.Error())
		} else {
			m.addLog(fmt.Sprintf("✓ Simulation done — %d rounds completed", m.simMaxRounds))
		}

	case reportSectionMsg:
		if msg.err != nil {
			m.reportRunning = false
			m.addLog("✗ Report error: " + msg.err.Error())
			break
		}
		if msg.section.Title != "" {
			m.reportSections = append(m.reportSections, msg.section)
			m.addLog(fmt.Sprintf("  §%d %s", msg.section.Index, msg.section.Title))
		}
		if msg.done {
			m.reportRunning = false
			// Build full markdown
			r := &report.Report{Scenario: m.simScenario, Sections: m.reportSections}
			m.reportContent = r.FormatMarkdown()
			m.viewport.SetContent(m.reportContent)
			m.addLog("✓ Report complete")
		}

	case interviewRespMsg:
		if msg.err != nil {
			m.addLog("✗ " + msg.err.Error())
		} else {
			m.chatLines = append(m.chatLines, S.Blue.Render(m.chatTarget)+": "+msg.resp)
			m.chatHistory = append(m.chatHistory, llm.Message{Role: "assistant", Content: msg.resp})
		}

	case logMsg:
		m.addLog(msg.text)

	case wizardProgressMsg:
		m.wizardMsg = msg.stage

	case wizardDoneMsg:
		m.wizardCh = nil // channel is exhausted
		if msg.err != nil {
			m.wizardMsg = "✗ Error: " + msg.err.Error()
			m.wizardStep = 1 // go back so user can retry
			m.addLog("✗ Wizard: " + msg.err.Error())
		} else {
			m.projectID = msg.projectID
			m.analyzeDir = "."
			nodes, _ := m.db.GetNodes(msg.projectID)
			edges, _ := m.db.GetEdges(msg.projectID)
			m.graphNodes = nodes
			m.graphEdges = edges
			if len(nodes) > 0 {
				m.chatTarget = nodes[0].Name
			}
			m.screen = screenGraph
			m.addLog("✓ Project initialized: " + msg.projectID)
		}

	case loadSessionsMsg:
		if msg.err != nil {
			m.addLog("✗ Sessions: " + msg.err.Error())
		} else {
			m.sessionList = msg.sessions
			if m.sessionIdx >= len(m.sessionList) {
				m.sessionIdx = 0
			}
		}

	case queryDataMsg:
		if msg.tab == m.queryTab {
			m.queryLines = msg.lines
			m.queryIdx = 0
		}

	case proxyStatusMsg:
		m.proxyOK = msg.ok
		m.proxyCheck = time.Now()
		// Schedule the next check in 30 seconds.
		if m.cfg.LLM.Provider == "clicliproxy" {
			baseURL := m.cfg.LLM.BaseURL
			if baseURL == "" {
				baseURL = "http://localhost:8080/v1"
			}
			cmds = append(cmds, tea.Tick(30*time.Second, func(t time.Time) tea.Msg {
				return checkProxyCmd(baseURL)()
			}))
		}

	case llmStatusMsg:
		m.llmStatusOK = msg.ok
		m.llmStatusMsg = msg.msg
		m.proxyOK = msg.ok // keep backward compat
		cfg := m.cfg
		cmds = append(cmds, tea.Tick(30*time.Second, func(t time.Time) tea.Msg {
			return checkLLMStatusCmd(cfg)()
		}))
	}

	// Update active input.
	// Note: wizard inputs are pre-updated in the KeyMsg case to ensure
	// handleWizardKey reads current values; skip them here for KeyMsgs.
	_, isKeyMsg := msg.(tea.KeyMsg)
	switch m.screen {
	case screenWizard:
		if !isKeyMsg {
			// For non-key messages (spinner ticks, window resize, etc.)
			// still forward to the text inputs so they can update their state.
			if m.wizardStep == 0 {
				var cmd tea.Cmd
				m.wizardName, cmd = m.wizardName.Update(msg)
				cmds = append(cmds, cmd)
			} else if m.wizardStep == 1 {
				var cmd tea.Cmd
				m.wizardDir, cmd = m.wizardDir.Update(msg)
				cmds = append(cmds, cmd)
			}
		}
	case screenSim:
		if !m.simRunning {
			var cmd tea.Cmd
			m.simInput, cmd = m.simInput.Update(msg)
			cmds = append(cmds, cmd)
		}
	case screenChat:
		if m.panelFocus == focusRight {
			var cmd tea.Cmd
			m.chatInput, cmd = m.chatInput.Update(msg)
			cmds = append(cmds, cmd)
		}
	case screenReport:
		if m.panelFocus == focusRight {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *App) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Wizard intercepts almost all keys while active.
	if m.screen == screenWizard {
		return m.handleWizardKey(msg)
	}

	// Help toggle — works everywhere.
	if msg.String() == "?" {
		if m.screen == screenHelp {
			m.screen = m.prevScreen
		} else {
			m.prevScreen = m.screen
			m.screen = screenHelp
		}
		return nil
	}

	// Global keys (only when help is not shown)
	if m.screen != screenHelp {
		switch msg.String() {
		case "ctrl+c":
			return tea.Quit
		case "D": // uppercase D to toggle debug info overlay
			if m.screen == screenDebug {
				m.screen = m.prevScreen
			} else {
				m.prevScreen = m.screen
				m.screen = screenDebug
				m.buildDebugInfo()
			}
			return nil
		case "q":
			if m.screen == screenDash {
				return tea.Quit
			}
			// [q] goes to query/history screen from any non-dash screen
			m.screen = screenQuery
			m.simInput.Blur()
			m.chatInput.Blur()
			return m.loadQueryData()
		case "s":
			m.screen = screenSession
			m.simInput.Blur()
			m.chatInput.Blur()
			return m.loadSessions()
		case "esc":
			if m.entitySearchMode {
				m.entitySearchMode = false
				m.entitySearch = ""
				return nil
			}
			m.screen = screenDash
			m.panelFocus = focusLeft
			return nil
		case "1":
			m.screen = screenGraph
			m.simInput.Blur()
			m.chatInput.Blur()
			return nil
		case "2":
			m.screen = screenEnv
			m.simInput.Blur()
			m.chatInput.Blur()
			m.panelFocus = focusLeft
			return nil
		case "3":
			m.screen = screenSim
			if !m.simRunning {
				m.simInput.Focus()
			}
			return nil
		case "4":
			m.screen = screenReport
			m.panelFocus = focusLeft
			return nil
		case "5":
			m.screen = screenChat
			m.panelFocus = focusLeft
			return nil
		}
	}

	// Screen-specific keys
	switch m.screen {
	case screenHelp:
		switch msg.String() {
		case "esc", "q":
			m.screen = m.prevScreen
		}

	case screenDebug:
		switch msg.String() {
		case "esc", "q":
			m.screen = m.prevScreen
		}

	case screenGraph:
		switch msg.String() {
		case "a", "enter":
			if !m.analyzeRunning {
				return m.startAnalyze()
			}
		case "w":
			return m.openWebViz()
		case "c":
			return m.runCommunity()
		}

	case screenEnv:
		if m.entitySearchMode {
			switch msg.String() {
			case "esc":
				m.entitySearchMode = false
				m.entitySearch = ""
			case "backspace":
				if len(m.entitySearch) > 0 {
					m.entitySearch = m.entitySearch[:len(m.entitySearch)-1]
				}
			case "enter":
				m.entitySearchMode = false
			default:
				if len(msg.String()) == 1 {
					m.entitySearch += msg.String()
				}
			}
			return nil
		}

		switch msg.String() {
		case "up", "k":
			if m.entityIdx > 0 {
				m.entityIdx--
			}
		case "down", "j":
			nodes := m.filteredNodes()
			if m.entityIdx < len(nodes)-1 {
				m.entityIdx++
			}
		case "/":
			m.entitySearchMode = true
			m.entitySearch = ""
		case "5", "enter":
			nodes := m.filteredNodes()
			if m.entityIdx < len(nodes) {
				m.chatTarget = nodes[m.entityIdx].Name
				// find global index for entityIdx tracking in chat
				for gi, gn := range m.graphNodes {
					if gn.Name == m.chatTarget {
						m.entityIdx = gi
						break
					}
				}
				m.screen = screenChat
				m.panelFocus = focusRight
				m.chatInput.Focus()
			}
		case "tab", "l", "right":
			m.panelFocus = focusRight
		case "h", "left":
			m.panelFocus = focusLeft
		}

	case screenSim:
		switch msg.String() {
		case "enter":
			if !m.simRunning && strings.TrimSpace(m.simInput.Value()) != "" {
				m.simScenario = strings.TrimSpace(m.simInput.Value())
				m.simInput.Reset()
				return m.startSim()
			}
		case "b":
			if !m.simRunning && strings.TrimSpace(m.simInput.Value()) != "" {
				m.simBackground = true
				m.simScenario = strings.TrimSpace(m.simInput.Value())
				m.simInput.Reset()
				return m.startSim()
			} else if !m.simRunning {
				m.simBackground = !m.simBackground
			}
		case "r":
			if m.simRounds < 50 {
				m.simRounds++
			}
		case "R":
			if m.simRounds > 1 {
				m.simRounds--
			}
		}

	case screenReport:
		switch msg.String() {
		case "r", "g":
			if !m.reportRunning && m.simScenario != "" {
				return m.startReport()
			}
		case "s":
			m.saveReport()
		case "tab", "l", "right":
			if len(m.reportSections) > 0 {
				m.panelFocus = focusRight
				// Set viewport content for selected section
				m.updateReportViewport()
			}
		case "h", "left":
			m.panelFocus = focusLeft
		case "up", "k":
			if m.panelFocus == focusLeft {
				if m.reportSectionIdx > 0 {
					m.reportSectionIdx--
					m.updateReportViewport()
				}
			}
		case "down", "j":
			if m.panelFocus == focusLeft {
				if m.reportSectionIdx < len(m.reportSections)-1 {
					m.reportSectionIdx++
					m.updateReportViewport()
				}
			}
		}

	case screenChat:
		switch msg.String() {
		case "enter":
			if m.panelFocus == focusRight {
				if q := strings.TrimSpace(m.chatInput.Value()); q != "" {
					m.chatInput.Reset()
					m.chatLines = append(m.chatLines, S.Muted.Render("you")+": "+q)
					return m.doInterview(q)
				}
			} else {
				// Enter from left panel selects agent
				nodes := m.graphNodes
				if m.entityIdx < len(nodes) {
					m.chatTarget = nodes[m.entityIdx].Name
					m.chatHistory = nil
					m.chatLines = nil
					m.panelFocus = focusRight
					m.chatInput.Focus()
				}
			}
		case "tab":
			// Cycle through agents
			if len(m.graphNodes) > 0 {
				m.entityIdx = (m.entityIdx + 1) % len(m.graphNodes)
				m.chatTarget = m.graphNodes[m.entityIdx].Name
				m.chatHistory = nil
				m.chatLines = nil
			}
		case "up", "k":
			if m.panelFocus == focusLeft {
				if m.entityIdx > 0 {
					m.entityIdx--
				}
			}
		case "down", "j":
			if m.panelFocus == focusLeft {
				if m.entityIdx < len(m.graphNodes)-1 {
					m.entityIdx++
				}
			}
		case "l", "right":
			m.panelFocus = focusRight
			m.chatInput.Focus()
		case "h", "left":
			m.panelFocus = focusLeft
			m.chatInput.Blur()
		}

	case screenSession:
		switch msg.String() {
		case "up", "k":
			if m.sessionIdx > 0 {
				m.sessionIdx--
			}
		case "down", "j":
			if m.sessionIdx < len(m.sessionList)-1 {
				m.sessionIdx++
			}
		case "enter":
			if m.sessionIdx < len(m.sessionList) {
				s := m.sessionList[m.sessionIdx]
				m.simScenario = s.Scenario
				m.simRounds = s.Rounds
				m.simMaxAgents = s.MaxAgents
				if len(s.Platforms) > 0 {
					m.simPlatforms = s.Platforms
				}
				m.screen = screenSim
				if !m.simRunning {
					m.simInput.SetValue(s.Scenario)
				}
				m.addLog("→ Loaded session: " + s.Name)
			}
		case "n":
			// Save current sim config as new session
			return m.saveCurrentSession()
		case "f":
			if m.sessionIdx < len(m.sessionList) {
				return m.forkSession(m.sessionList[m.sessionIdx].ID)
			}
		case "d":
			if m.sessionIdx < len(m.sessionList) {
				return m.deleteSession(m.sessionList[m.sessionIdx].ID)
			}
		}

	case screenQuery:
		switch msg.String() {
		case "tab":
			m.queryTab = (m.queryTab + 1) % 4
			return m.loadQueryData()
		case "up", "k":
			if m.queryIdx > 0 {
				m.queryIdx--
			}
		case "down", "j":
			if m.queryIdx < len(m.queryLines)-1 {
				m.queryIdx++
			}
		case "r":
			return m.loadQueryData()
		}
	}
	return nil
}

// updateReportViewport sets viewport content to the selected section.
func (m *App) updateReportViewport() {
	if m.reportSectionIdx < len(m.reportSections) {
		sec := m.reportSections[m.reportSectionIdx]
		m.viewport.SetContent(sec.Content)
		m.viewport.GotoTop()
	} else if m.reportContent != "" {
		m.viewport.SetContent(m.reportContent)
	}
}

// filteredNodes returns graphNodes filtered by entitySearch.
func (m *App) filteredNodes() []db.Node {
	if m.entitySearch == "" {
		return m.graphNodes
	}
	q := strings.ToLower(m.entitySearch)
	var out []db.Node
	for _, n := range m.graphNodes {
		if strings.Contains(strings.ToLower(n.Name), q) ||
			strings.Contains(strings.ToLower(n.Type), q) {
			out = append(out, n)
		}
	}
	return out
}

// ─── View ─────────────────────────────────────────────────────────────────────

func (m App) View() string {
	if m.width == 0 {
		return "Initializing…"
	}

	if m.screen == screenWizard {
		return m.viewWizard()
	}

	if m.screen == screenDebug {
		return m.viewDebug()
	}

	if m.screen == screenHelp {
		return m.viewHelp()
	}

	header := m.renderHeader()
	tabs := m.renderTabs()
	var body string

	switch m.screen {
	case screenDash:
		body = m.viewDash()
	case screenGraph:
		body = m.viewGraph()
	case screenEnv:
		body = m.viewEntities()
	case screenSim:
		body = m.viewSim()
	case screenReport:
		body = m.viewReport()
	case screenChat:
		body = m.viewChat()
	case screenSession:
		body = m.viewSession()
	case screenQuery:
		body = m.viewQuery()
	default:
		body = m.viewDash()
	}

	status := m.renderStatus()
	return lipgloss.JoinVertical(lipgloss.Left, header, tabs, body, status)
}

// ─── Header / Tabs / Status ──────────────────────────────────────────────────

func (m App) renderHeader() string {
	w := m.width
	project := S.Blue.Render(m.cfg.Project)
	model := S.Dim.Render(m.cfg.LLM.Provider + "/" + m.cfg.LLM.Model)

	// Simulation running indicator
	simIndicator := ""
	if m.simRunning {
		simIndicator = "  " + S.Red.Render("● sim running")
	} else if m.simDone {
		simIndicator = "  " + S.Green.Render("✓ sim done")
	}

	// LLM status indicator
	llmIndicator := ""
	if m.llmStatusMsg != "" {
		icon := "⬡"
		if m.llmStatusOK {
			llmIndicator = "  " + S.Green.Render(icon+" "+m.llmStatusMsg)
		} else {
			llmIndicator = "  " + S.Red.Render(icon+" "+m.llmStatusMsg)
		}
	}

	left := S.Bold.Render("fishnet") + "  ·  " + project + "  ·  " + model + simIndicator + llmIndicator

	// Right side: current time + hint
	now := time.Now().Format("15:04")
	right := S.Dim.Render("[q]quit  [esc]home  [?]help  " + now)

	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}
	return headerStyle.Width(w).Render(left + strings.Repeat(" ", gap) + right)
}

func (m App) renderTabs() string {
	// Map tab labels to their screens: first 5 are graph..chat (screens 1-5),
	// then sessions (7) and history/query (8).
	tabScreens := []screen{
		screenGraph, screenEnv, screenSim, screenReport, screenChat,
		screenSession, screenQuery,
	}
	var parts []string
	for i, label := range tabLabels {
		var s screen
		if i < len(tabScreens) {
			s = tabScreens[i]
		}
		if m.screen == s {
			parts = append(parts, tabActiveStyle.Render(label))
		} else {
			parts = append(parts, tabInactiveStyle.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func (m App) renderStatus() string {
	var hint string

	// Background sim indicator overrides per-screen hints when active.
	if m.simRunning && m.simBackground && m.screen != screenSim {
		elapsed := ""
		if !m.simStartTime.IsZero() {
			elapsed = "  elapsed: " + elapsedStr(time.Since(m.simStartTime))
		}
		hint = S.Yellow.Render("● Sim running in background") + S.Dim.Render("  · [3] watch feed  [ctrl+c] stop"+elapsed)
		return statusBarStyle.Width(m.width).Render("  " + hint)
	}

	switch m.screen {
	case screenDash:
		hint = "[1-5] switch tabs  [q] quit  [?] help"
	case screenGraph:
		hint = "[a] analyze docs  [c] community detection  [w] web viz  [esc] home"
	case screenEnv:
		if m.entitySearchMode {
			hint = "[esc] cancel search  [enter] confirm"
		} else {
			hint = "[↑↓/jk] navigate  [/] search  [enter] interview  [←→] switch panel  [esc] home"
		}
	case screenSim:
		if m.simRunning {
			elapsed := ""
			if !m.simStartTime.IsZero() {
				elapsed = "  elapsed: " + elapsedStr(time.Since(m.simStartTime))
			}
			hint = S.Yellow.Render("● Running") + S.Dim.Render(elapsed+"  [ctrl+c] stop")
		} else {
			hint = "[enter] run  [b] run in background  [r/R] rounds ±1  [esc] home"
		}
	case screenReport:
		hint = "[r/g] generate  [s] save report.md  [←→/hl] switch panel  [↑↓] navigate/scroll  [esc] home"
	case screenChat:
		hint = "[↑↓] select agent  [enter/l] chat  [tab] next agent  [h] agent list  [esc] home"
	case screenSession:
		hint = "[↑↓] navigate  [enter] load  [f] fork  [d] delete  [n] save current  [esc] home"
	case screenQuery:
		hint = "[tab] switch view  [↑↓] scroll  [r] refresh  [esc] home"
	case screenHelp:
		hint = "[esc/q] close help"
	}
	return statusBarStyle.Width(m.width).Render("  " + S.Dim.Render(hint))
}

// ─── Screen Views ─────────────────────────────────────────────────────────────

func (m App) viewDash() string {
	w := m.width - 4
	st := m.db.GetStats(m.projectID)

	half := (w - 3) / 2

	// ── Left box: Graph stats ───────────────────────────────────────────────
	graphContent := S.Blue.Render("Graph") + "\n" +
		fmt.Sprintf("  Nodes:       %s\n", S.Bold.Render(fmt.Sprint(st.Nodes))) +
		fmt.Sprintf("  Edges:       %s\n", S.Bold.Render(fmt.Sprint(st.Edges))) +
		fmt.Sprintf("  Communities: %s\n", S.Bold.Render(fmt.Sprint(st.Communities))) +
		fmt.Sprintf("  Documents:   %s", S.Bold.Render(fmt.Sprint(st.Documents)))
	graphBox := boxStyle.Width(half).Render(graphContent)

	// ── Right box: Last simulation ─────────────────────────────────────────
	var simContent string
	if m.simDone || m.simScenario != "" {
		roundStr := fmt.Sprintf("%d/%d", m.simRound, m.simMaxRounds)
		doneStr := ""
		if m.simDone {
			doneStr = " " + S.Green.Render("✓")
		}
		totalActions := len(m.simActions)
		simContent = S.Blue.Render("Last Simulation") + "\n" +
			fmt.Sprintf("  Scenario:  %s\n", S.Muted.Render(clip(m.simScenario, half-14))) +
			fmt.Sprintf("  Rounds:    %s%s\n", S.Bold.Render(roundStr), doneStr) +
			fmt.Sprintf("  Posts:     %s\n", S.Bold.Render(fmt.Sprint(m.simTwStat.Posts+m.simRdStat.Posts))) +
			fmt.Sprintf("  Actions:   %s", S.Bold.Render(fmt.Sprint(totalActions)))
	} else {
		simContent = S.Blue.Render("Last Simulation") + "\n" +
			"  " + S.Dim.Render("No simulation yet.") + "\n" +
			"  " + S.Dim.Render("Press [3] to run one.")
	}
	simBox := boxStyle.Width(half).Render(simContent)

	statsRow := lipgloss.JoinHorizontal(lipgloss.Top, graphBox, "  ", simBox)

	// ── Quick start ─────────────────────────────────────────────────────────
	qs := S.Bold.Render("  QUICK START") + "\n"
	steps := []struct{ num, desc string }{
		{"1", "Build knowledge graph from documents"},
		{"2", "Browse & configure agent personas"},
		{"3", "Run Twitter + Reddit simulation"},
		{"4", "Generate analysis report"},
		{"5", "Interview agents interactively"},
	}
	for _, s := range steps {
		num := S.Blue.Render("[" + s.num + "]")
		qs += "  " + num + " " + S.Muted.Render(s.desc) + "\n"
	}

	// ── Recent activity ─────────────────────────────────────────────────────
	sep := "\n  " + S.Dim.Render("RECENT ACTIVITY "+strings.Repeat("─", w-18))
	logs := "\n" + m.renderLogs(10)

	return lipgloss.JoinVertical(lipgloss.Left,
		"",
		"  "+S.Bold.Render("PROJECT STATUS"),
		"  "+statsRow,
		"",
		qs,
		sep,
		logs,
	)
}

func (m App) viewGraph() string {
	w := m.width - 4
	heading := S.Bold.Render("Step 1 — Build Knowledge Graph")

	st := m.db.GetStats(m.projectID)

	// Determine sub-step states
	// Sub-step 01: Ontology — heuristic: done if we have varied node types
	ontologyDone := false
	typeSet := make(map[string]struct{})
	for _, n := range m.graphNodes {
		if n.Type != "" {
			typeSet[n.Type] = struct{}{}
		}
	}
	if len(typeSet) >= 2 {
		ontologyDone = true
	}

	// Sub-step 02: GraphRAG Build
	graphRunning := m.analyzeRunning
	graphDone := len(m.graphNodes) > 0 && !m.analyzeRunning

	// Sub-step 03: Community Detection
	communityDone := false
	for _, n := range m.graphNodes {
		if n.CommunityID >= 0 {
			communityDone = true
			break
		}
	}

	cardW := w - 4

	// ── Sub-step 01: Ontology Generation ──────────────────────────────────
	var card01 string
	{
		var statusLine string
		if ontologyDone {
			var typeList []string
			for t := range typeSet {
				typeList = append(typeList, t)
			}
			statusLine = S.Green.Render("[COMPLETED] ✓")
			// Collect entity/edge type samples
			entityTypes := ""
			for i, t := range typeList {
				if i >= 5 {
					entityTypes += " …"
					break
				}
				if i > 0 {
					entityTypes += "  "
				}
				entityTypes += S.Blue.Render(t)
			}
			// Edge types
			edgeSet := make(map[string]struct{})
			for _, e := range m.graphEdges {
				if e.Type != "" {
					edgeSet[e.Type] = struct{}{}
				}
			}
			edgeTypes := ""
			ei := 0
			for et := range edgeSet {
				if ei >= 5 {
					edgeTypes += " …"
					break
				}
				if ei > 0 {
					edgeTypes += "  "
				}
				edgeTypes += S.Dim.Render(et)
				ei++
			}
			body := statusLine + "\n"
			body += "  Entity types: " + entityTypes + "\n"
			if edgeTypes != "" {
				body += "  Edge types:   " + edgeTypes
			}
			card01 = boxStyleGreen.Width(cardW).Render(
				S.Bold.Render("01 Ontology Generation") + "\n" + body)
		} else {
			statusLine = S.Dim.Render("[pending]  Press [a] to analyze documents")
			card01 = boxStyle.Width(cardW).Render(
				S.Bold.Render("01 Ontology Generation") + "\n" + statusLine)
		}
	}

	// ── Sub-step 02: GraphRAG Build ──────────────────────────────────────
	var card02 string
	{
		var body string
		if graphRunning {
			pct := 0
			if m.analyzeProg.total > 0 {
				pct = int(float64(m.analyzeProg.done) / float64(m.analyzeProg.total) * 100)
			}
			bar := progressBar(pct, 20)
			body = S.Yellow.Render(fmt.Sprintf("[RUNNING %d%%]", pct)) + " " + bar + "\n" +
				fmt.Sprintf("  %s Extracting… +%d nodes  +%d edges",
					m.spinner.View(), m.analyzeProg.nodes, m.analyzeProg.edges) + "\n" +
				"  Source dir: " + S.Blue.Render(m.analyzeDir)
			card02 = boxStyleTeal.Width(cardW).Render(
				S.Bold.Render("02 GraphRAG Build") + "\n" + body)
		} else if m.analyzeProg.err != nil {
			body = S.Red.Render("✗ "+m.analyzeProg.err.Error()) + "\n" +
				"  Source dir: " + S.Blue.Render(m.analyzeDir)
			card02 = boxStyle.Width(cardW).Render(
				S.Bold.Render("02 GraphRAG Build") + "\n" + body)
		} else if graphDone {
			extraWarn := ""
			if m.analyzeProg.final && m.analyzeProg.nodes == 0 && m.analyzeProg.err == nil && !m.analyzeRunning {
				extraWarn = "\n  " + S.Yellow.Render("⚠ No nodes extracted — check provider config ([D] for details)")
			}
			body = S.Green.Render("[COMPLETED] ✓") + fmt.Sprintf("  %d nodes, %d edges", st.Nodes, st.Edges) + "\n" +
				"  Source dir: " + S.Blue.Render(m.analyzeDir) + extraWarn
			card02 = boxStyleGreen.Width(cardW).Render(
				S.Bold.Render("02 GraphRAG Build") + "\n" + body)
		} else {
			body = S.Dim.Render("[PENDING]  Press [a] to start") + "\n" +
				"  Source dir: " + S.Blue.Render(m.analyzeDir)
			card02 = boxStyle.Width(cardW).Render(
				S.Bold.Render("02 GraphRAG Build") + "\n" + body)
		}
	}

	// ── Sub-step 03: Community Detection ────────────────────────────────
	var card03 string
	{
		var body string
		if communityDone {
			body = S.Green.Render("[COMPLETED] ✓") + fmt.Sprintf("  %d communities found", st.Communities)
			card03 = boxStyleGreen.Width(cardW).Render(
				S.Bold.Render("03 Community Detection") + "\n" + body)
		} else {
			body = S.Dim.Render("[PENDING]  Press [c] to run")
			card03 = boxStyle.Width(cardW).Render(
				S.Bold.Render("03 Community Detection") + "\n" + body)
		}
	}

	actionsRow := "  " +
		S.Green.Render("[a/enter]") + S.Dim.Render(" Analyze  ") +
		S.Blue.Render("[c]") + S.Dim.Render(" Communities  ") +
		S.Purple.Render("[w]") + S.Dim.Render(" Web Viz")

	sep := S.Dim.Render("  " + strings.Repeat("─", w-4))
	logs := m.renderLogs(6)
	return lipgloss.JoinVertical(lipgloss.Left,
		"",
		"  "+heading,
		"",
		"  "+card01,
		"  "+card02,
		"  "+card03,
		"",
		actionsRow,
		"",
		sep,
		logs,
	)
}

func (m App) viewEntities() string {
	w := m.width - 4
	nodes := m.filteredNodes()

	heading := S.Bold.Render("Step 2 — Agent Personas")
	countStr := S.Dim.Render(fmt.Sprintf("(%d agents loaded)", len(nodes)))
	if m.entitySearch != "" {
		countStr = S.Yellow.Render(fmt.Sprintf("Search: %q  (%d match)", m.entitySearch, len(nodes)))
	}

	if len(m.graphNodes) == 0 {
		return "\n  " + heading + "\n\n  " +
			S.Yellow.Render("No entities yet. Build the graph first: [1]")
	}

	// Clamp entityIdx to filtered list size
	if m.entityIdx >= len(nodes) && len(nodes) > 0 {
		m.entityIdx = len(nodes) - 1
	}

	leftW := (w * 40) / 100
	rightW := w - leftW - 3

	// ── Left panel: agent list ──────────────────────────────────────────────
	panelH := m.height - 8
	start := m.entityIdx - (panelH / 2)
	if start < 0 {
		start = 0
	}
	end := start + panelH
	if end > len(nodes) {
		end = len(nodes)
	}

	var listLines []string
	for i := start; i < end; i++ {
		n := nodes[i]
		prefix := "  "
		nameStyle := S.Muted
		typeStr := S.Dim.Render(clip(n.Type, 8))
		if i == m.entityIdx {
			prefix = "> "
			nameStyle = S.White
		}
		line := prefix + nameStyle.Render(clip(n.Name, leftW-13)) + "  " + typeStr
		if i == m.entityIdx {
			listLines = append(listLines, selectedStyle.Width(leftW-2).Render(line))
		} else {
			listLines = append(listLines, line)
		}
	}

	hintLine := "\n" + S.Dim.Render("  [↑↓] navigate") + "\n" +
		S.Dim.Render("  [5/enter] interview") + "\n" +
		S.Dim.Render("  [/] search")

	leftContent := strings.Join(listLines, "\n") + hintLine
	leftTitle := "Agents"
	if m.entitySearchMode {
		leftTitle = "Search: " + m.entitySearch + "█"
	}

	var leftPanel string
	if m.panelFocus == focusLeft {
		leftPanel = leftPanelActiveStyle.Width(leftW).Height(panelH).Render(
			S.Blue.Render(leftTitle) + "\n" + leftContent)
	} else {
		leftPanel = leftPanelStyle.Width(leftW).Height(panelH).Render(
			S.Muted.Render(leftTitle) + "\n" + leftContent)
	}

	// ── Right panel: entity detail ──────────────────────────────────────────
	var rightContent string
	if m.entityIdx < len(nodes) {
		n := nodes[m.entityIdx]
		rightContent = m.renderEntityDetail(n, rightW)
	} else {
		rightContent = S.Dim.Render("  Select an agent to view details.")
	}

	rightTitle := "Selected: "
	if m.entityIdx < len(nodes) {
		rightTitle += nodes[m.entityIdx].Name
	}

	var rightPanel string
	if m.panelFocus == focusRight {
		rightPanel = rightPanelActiveStyle.Width(rightW).Height(panelH).Render(
			S.Blue.Render(rightTitle) + "\n" + rightContent)
	} else {
		rightPanel = rightPanelStyle.Width(rightW).Height(panelH).Render(
			S.Muted.Render(rightTitle) + "\n" + rightContent)
	}

	splitRow := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, " ", rightPanel)

	return "\n  " + heading + "  " + countStr + "\n\n  " + splitRow
}

// renderEntityDetail renders the detail card for an entity node.
func (m App) renderEntityDetail(n db.Node, w int) string {
	var sb strings.Builder

	// Type and community
	comm := ""
	if n.CommunityID >= 0 {
		comm = "  " + S.Dim.Render(fmt.Sprintf("[community #%d]", n.CommunityID))
	}
	sb.WriteString(fmt.Sprintf("  Type:      %s%s\n", S.Blue.Render(n.Type), comm))

	// Summary
	summary := n.Summary
	if summary == "" {
		summary = S.Dim.Render("(no summary)")
	} else {
		// Wrap summary lines
		words := strings.Fields(summary)
		lineW := w - 4
		var line string
		var lines []string
		for _, word := range words {
			if len(line)+len(word)+1 > lineW {
				lines = append(lines, line)
				line = word
			} else {
				if line == "" {
					line = word
				} else {
					line += " " + word
				}
			}
		}
		if line != "" {
			lines = append(lines, line)
		}
		summary = strings.Join(lines, "\n  ")
	}
	sb.WriteString(fmt.Sprintf("  Summary:   %s\n", S.Muted.Render(summary)))
	sb.WriteString("\n")

	// Parse attributes JSON for personality traits
	attrs := parseAttrs(n.Attributes)
	barW := 10

	// Stance
	stance, ok := attrs["stance"]
	if !ok {
		stance = "neutral"
	}
	sb.WriteString(fmt.Sprintf("  Stance:    %s\n", stanceStyle(stance)))

	// Profession
	if prof, ok := attrs["profession"]; ok && prof != "" {
		sb.WriteString(fmt.Sprintf("  Profession: %s\n", S.Muted.Render(prof)))
	}

	// Sentiment bias
	if sentBias, ok := attrs["sentiment_bias"]; ok && sentBias != "" {
		sb.WriteString(fmt.Sprintf("  Sentiment bias: %s\n", S.Yellow.Render(sentBias)))
	}

	// Posts per hour
	if pph, ok := attrs["posts_per_hour"]; ok && pph != "" {
		sb.WriteString(fmt.Sprintf("  Posts/hour: %s\n", S.Teal.Render(pph)))
	}
	sb.WriteString("\n")

	// Numeric traits — expanded
	type trait struct {
		key   string
		label string
	}
	traits := []trait{
		{"activity", "Activity"},
		{"sentiment", "Sentiment"},
		{"influence", "Influence"},
		{"originality", "Originality"},
		{"agreeableness", "Agreeableness"},
		{"neuroticism", "Neuroticism"},
		{"openness", "Openness"},
	}
	for _, t := range traits {
		val := 0.5 // default
		if v, ok2 := attrs[t.key]; ok2 {
			fmt.Sscanf(v, "%f", &val)
		} else {
			continue // only show if attribute exists
		}
		sb.WriteString(traitBar(t.label, val, barW) + "\n")
	}
	// Always show core 4 even if missing
	coreTraits := []trait{
		{"activity", "Activity"},
		{"sentiment", "Sentiment"},
		{"influence", "Influence"},
		{"originality", "Originality"},
	}
	for _, t := range coreTraits {
		if _, exists := attrs[t.key]; !exists {
			sb.WriteString(traitBar(t.label, 0.5, barW) + "\n")
		}
	}

	// Topics of interest
	if topics, ok := attrs["topics"]; ok && topics != "" {
		sb.WriteString("\n  " + S.Blue.Render("Topics:") + " " + S.Muted.Render(clip(topics, w-12)) + "\n")
	}

	sb.WriteString("\n")

	// Sim config summary at bottom
	platforms := ""
	for i, p := range m.simPlatforms {
		if i > 0 {
			platforms += " + "
		}
		switch p {
		case "twitter":
			platforms += twitterColor.Render("Twitter")
		case "reddit":
			platforms += redditColor.Render("Reddit")
		default:
			platforms += p
		}
	}
	sb.WriteString(S.Dim.Render("  ── Sim Config ──") + "\n")
	sb.WriteString(fmt.Sprintf("  Rounds: %s  Platforms: %s\n",
		S.Bold.Render(fmt.Sprint(m.simRounds)),
		platforms))
	sb.WriteString("  " + S.Dim.Render("Press [3] to configure and run simulation") + "\n")
	sb.WriteString("\n")
	sb.WriteString("  " + S.Dim.Render("[enter] Interview this agent"))

	return sb.String()
}

// parseAttrs parses a JSON attributes string into a map of string→string.
func parseAttrs(raw string) map[string]string {
	out := make(map[string]string)
	if raw == "" || raw == "{}" {
		return out
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return out
	}
	for k, v := range m {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}

func (m App) viewSim() string {
	if m.simRunning {
		return m.viewSimRunning()
	}

	heading := S.Bold.Render("Step 3 — Platform Simulation")
	if m.simDone {
		heading = S.Green.Render("✓") + " " + heading + " " + S.Dim.Render("(complete)")
	}

	w := m.width - 4
	sep := S.Dim.Render(strings.Repeat("─", w))

	// Scenario input box
	scenarioLabel := S.Muted.Render("Scenario")
	if m.simScenario != "" && m.simDone {
		scenarioLabel += "  " + S.Dim.Render("last: "+clip(m.simScenario, 50))
	}
	scenarioBox := boxStyle.Width(w).Render(
		scenarioLabel + "\n  " + m.simInput.View())

	// Configuration panel
	bgFlag := ""
	if m.simBackground {
		bgFlag = "  " + S.Yellow.Render("[bg]")
	}
	platforms := ""
	for i, p := range m.simPlatforms {
		if i > 0 {
			platforms += S.Dim.Render(" + ")
		}
		switch p {
		case "twitter":
			platforms += twitterColor.Render("● Twitter")
		case "reddit":
			platforms += redditColor.Render("● Reddit")
		default:
			platforms += S.Blue.Render(p)
		}
	}
	agentCount := m.simMaxAgents
	if len(m.graphNodes) > 0 && len(m.graphNodes) < agentCount {
		agentCount = len(m.graphNodes)
	}

	cfgBlock := "  " + S.Bold.Render("Settings") + "\n" +
		fmt.Sprintf("    %s  %s\n",
			S.Muted.Render("Rounds:   "), S.Bold.Render(fmt.Sprint(m.simRounds))+S.Dim.Render("  (press r/R to adjust)")) +
		fmt.Sprintf("    %s  %s\n",
			S.Muted.Render("Agents:   "), S.Bold.Render(fmt.Sprint(agentCount))+S.Dim.Render(" (all from graph)")) +
		fmt.Sprintf("    %s  %s",
			S.Muted.Render("Platforms:"), platforms)

	actions := "\n  " + S.Green.Render("[enter]") + S.Dim.Render(" Start simulation    ") +
		S.Yellow.Render("[b]") + S.Dim.Render(" Run in background") + bgFlag

	return "\n  " + heading + "\n\n" +
		scenarioBox + "\n\n" +
		cfgBlock + "\n\n" +
		"  " + sep +
		actions
}

func (m App) viewSimRunning() string {
	w := m.width - 2
	half := (w - 4) / 2

	pct := 0
	if m.simMaxRounds > 0 {
		pct = m.simRound * 100 / m.simMaxRounds
	}

	// ── Elapsed time ─────────────────────────────────────────────────────────
	elapsed := ""
	if !m.simStartTime.IsZero() {
		elapsed = elapsedStr(time.Since(m.simStartTime))
	}

	// ── Round progress bar (top) ─────────────────────────────────────────
	bar := progressBar(pct, 20)
	spinSuffix := ""
	if !m.simDone {
		spinSuffix = "  " + m.spinner.View()
	} else {
		spinSuffix = "  " + S.Green.Render("✓ Done")
	}
	roundHeader := fmt.Sprintf("  Round %s/%s  [%s] %d%%%s          Elapsed: %s",
		S.Bold.Render(fmt.Sprint(m.simRound)),
		S.Muted.Render(fmt.Sprint(m.simMaxRounds)),
		bar, pct, spinSuffix,
		S.Muted.Render(elapsed))

	// ── Platform stat boxes (Twitter | Reddit) ────────────────────────────
	twStats := fmt.Sprintf("Posts: %s  Likes: %s  RT: %s",
		S.Bold.Render(fmt.Sprint(m.simTwStat.Posts)),
		S.Bold.Render(fmt.Sprint(m.simTwStat.Likes)),
		S.Bold.Render(fmt.Sprint(m.simTwStat.Reposts)))

	rdStats := fmt.Sprintf("Posts: %s  Cmts: %s  Up: %s",
		S.Bold.Render(fmt.Sprint(m.simRdStat.Posts)),
		S.Bold.Render(fmt.Sprint(m.simRdStat.Comments)),
		S.Bold.Render(fmt.Sprint(m.simRdStat.Likes)))

	// ── Dual-column feed ─────────────────────────────────────────────────
	colW := (half) - 2
	contentW := colW - 22
	if contentW < 15 {
		contentW = 15
	}

	renderFeedEntry := func(a platform.Action, handlePrefix string, nameStyle lipgloss.Style) string {
		handle := nameStyle.Render(handlePrefix + clip(platform.SafeUsername(a.AgentName), 12))
		icon := actionIcon(a.Type)
		excerpt := ""
		if a.Content != "" {
			excerpt = S.Muted.Render(`"` + clip(a.Content, contentW) + `"`)
		} else {
			excerpt = S.Dim.Render(a.Type)
		}
		return fmt.Sprintf("%-16s %s %s", handle, icon, excerpt)
	}

	// Last 8 from each platform
	twStart := len(m.simTwActions) - 8
	if twStart < 0 {
		twStart = 0
	}
	rdStart := len(m.simRdActions) - 8
	if rdStart < 0 {
		rdStart = 0
	}
	twFeed := m.simTwActions[twStart:]
	rdFeed := m.simRdActions[rdStart:]

	var twLines, rdLines []string
	for _, a := range twFeed {
		twLines = append(twLines, renderFeedEntry(a, "@", twitterColor))
	}
	for _, a := range rdFeed {
		rdLines = append(rdLines, renderFeedEntry(a, "u/", redditColor))
	}

	// Pad to same length for side-by-side display
	maxLines := len(twLines)
	if len(rdLines) > maxLines {
		maxLines = len(rdLines)
	}
	for len(twLines) < maxLines {
		twLines = append(twLines, "")
	}
	for len(rdLines) < maxLines {
		rdLines = append(rdLines, "")
	}
	if maxLines == 0 {
		twLines = []string{S.Dim.Render("  Waiting for agents…")}
		rdLines = []string{S.Dim.Render("  Waiting for agents…")}
		maxLines = 1
	}

	twHeader := twitterColor.Bold(true).Render("Twitter") + "  " + S.Dim.Render(twStats)
	rdHeader := redditColor.Bold(true).Render("Reddit") + "  " + S.Dim.Render(rdStats)

	twBoxLines := twHeader + "\n" + strings.Join(twLines, "\n")
	rdBoxLines := rdHeader + "\n" + strings.Join(rdLines, "\n")

	twBox := boxStyleTeal.Copy().Width(half).Render(twBoxLines)
	rdBox := boxStyleOrange.Copy().Width(half).Render(rdBoxLines)

	dualCols := lipgloss.JoinHorizontal(lipgloss.Top, twBox, "  ", rdBox)

	// ── Bottom summary bar ────────────────────────────────────────────────
	totalActions := len(m.simActions)
	twCount := len(m.simTwActions)
	rdCount := len(m.simRdActions)
	activeAgents := m.simTwStat.Users + m.simRdStat.Users
	summaryBar := S.Dim.Render(fmt.Sprintf(
		"  Total: %d events (tw:%d / rd:%d)  Agents: %d",
		totalActions, twCount, rdCount, activeAgents))

	nav := ""
	if m.simDone {
		nav = "\n  " + S.Green.Render("✓ Simulation complete!") + "  " +
			S.Dim.Render("[4] Generate Report")
	}

	return "\n" + roundHeader + "\n\n" +
		"  " + dualCols + "\n\n" +
		summaryBar + nav
}

func (m App) viewReport() string {
	w := m.width - 4
	heading := S.Bold.Render("Step 4 — Analysis Report")

	if len(m.reportSections) == 0 && !m.reportRunning {
		hint := "\n  " + S.Yellow.Render("No report yet.")
		if m.simScenario == "" {
			hint += " Run simulation first: [3]"
		} else {
			hint += " Press [r] to generate."
		}
		return "\n  " + heading + hint
	}

	panelH := m.height - 8
	leftW := (w * 30) / 100
	rightW := w - leftW - 3

	// ── Left: section list ──────────────────────────────────────────────────
	var sectionLines []string

	// Expected sections (if report is running we show placeholders)
	expectedTitles := []string{
		"Key Actors",
		"Relationships",
		"Outcomes",
		"Insights",
	}
	// If we have real sections, use them
	if len(m.reportSections) > 0 {
		for i, sec := range m.reportSections {
			icon := " "
			var style lipgloss.Style
			if i == m.reportSectionIdx {
				icon = ">"
				style = sectionActiveStyle
			} else {
				icon = "✓"
				style = sectionDoneStyle
			}
			line := fmt.Sprintf(" %s §%d %s", icon, sec.Index, clip(sec.Title, leftW-8))
			sectionLines = append(sectionLines, style.Render(line))
		}
		// Show pending sections if report is still running
		if m.reportRunning {
			for i := len(m.reportSections); i < len(expectedTitles); i++ {
				if i == len(m.reportSections) {
					line := fmt.Sprintf(" %s §%d %s", m.spinner.View(), i+1,
						clip(expectedTitles[i], leftW-8))
					sectionLines = append(sectionLines, sectionActiveStyle.Render(line))
				} else {
					line := fmt.Sprintf("   §%d %s", i+1, clip(expectedTitles[i], leftW-8))
					sectionLines = append(sectionLines, sectionPendingStyle.Render(line))
				}
			}
		}
	} else if m.reportRunning {
		sectionLines = append(sectionLines, sectionActiveStyle.Render(" "+m.spinner.View()+" Generating…"))
		for i, t := range expectedTitles {
			line := fmt.Sprintf("   §%d %s", i+1, t)
			sectionLines = append(sectionLines, sectionPendingStyle.Render(line))
		}
	}

	sectionLines = append(sectionLines, "")
	sectionLines = append(sectionLines, S.Dim.Render(" [r] Generate"))
	sectionLines = append(sectionLines, S.Dim.Render(" [s] Save MD"))

	leftContent := strings.Join(sectionLines, "\n")
	var leftPanel string
	if m.panelFocus == focusLeft {
		leftPanel = leftPanelActiveStyle.Width(leftW).Height(panelH).Render(
			S.Blue.Render("Sections") + "\n" + leftContent)
	} else {
		leftPanel = leftPanelStyle.Width(leftW).Height(panelH).Render(
			S.Muted.Render("Sections") + "\n" + leftContent)
	}

	// ── Right: content viewport ─────────────────────────────────────────────
	m.viewport.Width = rightW - 2
	m.viewport.Height = panelH - 2

	var rightTitle string
	if m.reportSectionIdx < len(m.reportSections) {
		rightTitle = "Content — " + m.reportSections[m.reportSectionIdx].Title
	} else {
		rightTitle = "Content"
	}

	var rightInner string
	if m.panelFocus == focusRight && len(m.reportSections) > 0 {
		rightInner = m.viewport.View() + "\n" + S.Dim.Render("[↑↓/PgUp/PgDn] scroll")
	} else if len(m.reportSections) > 0 {
		rightInner = m.viewport.View()
	} else if m.reportRunning {
		rightInner = "\n  " + m.spinner.View() + " " + S.Yellow.Render("Generating report sections…") +
			"\n\n" + m.renderLogs(6)
	} else {
		rightInner = S.Dim.Render("  No content yet.")
	}

	var rightPanel string
	if m.panelFocus == focusRight {
		rightPanel = rightPanelActiveStyle.Width(rightW).Height(panelH).Render(
			S.Blue.Render(rightTitle) + "\n" + rightInner)
	} else {
		rightPanel = rightPanelStyle.Width(rightW).Height(panelH).Render(
			S.Muted.Render(rightTitle) + "\n" + rightInner)
	}

	splitRow := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, " ", rightPanel)
	return "\n  " + heading + "\n\n  " + splitRow
}

func (m App) viewChat() string {
	w := m.width - 4
	heading := S.Bold.Render("Step 5 — Agent Interview")

	panelH := m.height - 8
	leftW := (w * 30) / 100
	rightW := w - leftW - 3

	// ── Left panel: report summary + agent list ────────────────────────────
	var leftLines []string

	// Report summary (top of left panel) — show if we have sections
	if len(m.reportSections) > 0 {
		leftLines = append(leftLines, S.Blue.Render("  Report Summary"))
		maxSecs := 3
		if len(m.reportSections) < maxSecs {
			maxSecs = len(m.reportSections)
		}
		for i := 0; i < maxSecs; i++ {
			sec := m.reportSections[i]
			leftLines = append(leftLines, S.Green.Render("  ✓ ")+S.Dim.Render(clip(sec.Title, leftW-6)))
		}
		if len(m.reportSections) > 3 {
			leftLines = append(leftLines, S.Dim.Render(fmt.Sprintf("  … +%d sections", len(m.reportSections)-3)))
		}
		leftLines = append(leftLines, S.Dim.Render("  "+strings.Repeat("─", leftW-4)))
	}

	if len(m.graphNodes) == 0 {
		leftLines = append(leftLines, S.Yellow.Render("  No agents."))
		leftLines = append(leftLines, S.Dim.Render("  Build graph first: [1]"))
	} else {
		reportRows := len(leftLines)
		listH := panelH - 5 - reportRows
		if listH < 3 {
			listH = 3
		}
		start := m.entityIdx - listH/2
		if start < 0 {
			start = 0
		}
		end := start + listH
		if end > len(m.graphNodes) {
			end = len(m.graphNodes)
		}
		for i := start; i < end; i++ {
			n := m.graphNodes[i]
			typeStr := S.Dim.Render(clip(n.Type, 7))
			if i == m.entityIdx {
				line := fmt.Sprintf("> %s  %s", clip(n.Name, leftW-12), typeStr)
				leftLines = append(leftLines, selectedStyle.Width(leftW-2).Render(line))
			} else {
				leftLines = append(leftLines, S.Muted.Render(fmt.Sprintf("  %s  %s", clip(n.Name, leftW-12), typeStr)))
			}
		}
	}
	leftLines = append(leftLines, "")
	leftLines = append(leftLines, S.Dim.Render("  [tab] next agent"))
	leftLines = append(leftLines, S.Dim.Render("  [↑↓] select"))

	leftContent := strings.Join(leftLines, "\n")
	var leftPanel string
	if m.panelFocus == focusLeft {
		leftPanel = leftPanelActiveStyle.Width(leftW).Height(panelH).Render(
			S.Blue.Render("Agents") + "\n" + leftContent)
	} else {
		leftPanel = leftPanelStyle.Width(leftW).Height(panelH).Render(
			S.Muted.Render("Agents") + "\n" + leftContent)
	}

	// ── Right panel: chat ───────────────────────────────────────────────────
	chatTitle := "Chat"
	if m.chatTarget != "" {
		chatTitle = "Chat: " + m.chatTarget
	}

	// Chat history
	chatH := panelH - 5
	lines := m.chatLines
	if len(lines) > chatH {
		lines = lines[len(lines)-chatH:]
	}

	var chatText string
	if len(lines) == 0 {
		chatText = S.Dim.Render("  Start the conversation…\n")
	} else {
		var sb strings.Builder
		for _, l := range lines {
			sb.WriteString("  " + l + "\n")
		}
		chatText = sb.String()
	}

	m.chatInput.Width = rightW - 6

	sep := S.Dim.Render("  " + strings.Repeat("─", rightW-4))
	inputPrompt := "  > " + m.chatInput.View()

	var rightContent string
	if m.chatTarget == "" && len(m.graphNodes) > 0 {
		rightContent = "\n  " + S.Yellow.Render("Select an agent from the left panel.")
	} else if m.chatTarget == "" {
		rightContent = "\n  " + S.Yellow.Render("No agents yet. Analyze documents first: [1]")
	} else {
		rightContent = chatText + "\n" + sep + "\n" + inputPrompt
	}

	var rightPanel string
	if m.panelFocus == focusRight {
		rightPanel = rightPanelActiveStyle.Width(rightW).Height(panelH).Render(
			S.Blue.Render(chatTitle) + "\n" + rightContent)
	} else {
		rightPanel = rightPanelStyle.Width(rightW).Height(panelH).Render(
			S.Muted.Render(chatTitle) + "\n" + rightContent)
	}

	splitRow := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, " ", rightPanel)
	return "\n  " + heading + "\n\n  " + splitRow
}

func (m App) viewHelp() string {
	w := m.width
	h := m.height

	content := S.Bold.Render("FISHNET KEYBINDINGS") + "\n\n" +
		S.Blue.Render("Global") + "\n" +
		S.Muted.Render("  1-5      ") + S.Dim.Render("Switch to tab") + "\n" +
		S.Muted.Render("  s        ") + S.Dim.Render("Sessions manager") + "\n" +
		S.Muted.Render("  q        ") + S.Dim.Render("Query / history browser (or quit from dash)") + "\n" +
		S.Muted.Render("  esc      ") + S.Dim.Render("Return to dashboard") + "\n" +
		S.Muted.Render("  ctrl+c   ") + S.Dim.Render("Force quit") + "\n" +
		S.Muted.Render("  ?        ") + S.Dim.Render("Toggle help") + "\n" +
		S.Muted.Render("  D        ") + S.Dim.Render("Debug info") + "\n\n" +

		S.Blue.Render("Graph  (Tab 1)") + "\n" +
		S.Muted.Render("  a/enter  ") + S.Dim.Render("Analyze docs (01 Ontology + 02 GraphRAG)") + "\n" +
		S.Muted.Render("  c        ") + S.Dim.Render("03 Community detection") + "\n" +
		S.Muted.Render("  w        ") + S.Dim.Render("Open web visualization") + "\n\n" +

		S.Blue.Render("Entities  (Tab 2)") + "\n" +
		S.Muted.Render("  ↑↓ / jk  ") + S.Dim.Render("Navigate agent list") + "\n" +
		S.Muted.Render("  /        ") + S.Dim.Render("Search agents") + "\n" +
		S.Muted.Render("  enter    ") + S.Dim.Render("Interview selected agent") + "\n" +
		S.Muted.Render("  ←→ / hl  ") + S.Dim.Render("Switch panel focus") + "\n\n" +

		S.Blue.Render("Simulation  (Tab 3)") + "\n" +
		S.Muted.Render("  enter    ") + S.Dim.Render("Run simulation with scenario") + "\n" +
		S.Muted.Render("  r / R    ") + S.Dim.Render("Rounds +1 / -1") + "\n" +
		S.Muted.Render("  b        ") + S.Dim.Render("Run in background (navigate freely)") + "\n\n" +

		S.Blue.Render("Report  (Tab 4)") + "\n" +
		S.Muted.Render("  r / g    ") + S.Dim.Render("Generate report") + "\n" +
		S.Muted.Render("  s        ") + S.Dim.Render("Save as report.md") + "\n" +
		S.Muted.Render("  ↑↓       ") + S.Dim.Render("Navigate sections (left) / scroll (right)") + "\n" +
		S.Muted.Render("  ←→ / hl  ") + S.Dim.Render("Switch panel focus") + "\n\n" +

		S.Blue.Render("Interview  (Tab 5)") + "\n" +
		S.Muted.Render("  enter    ") + S.Dim.Render("Select agent / send message") + "\n" +
		S.Muted.Render("  tab      ") + S.Dim.Render("Cycle to next agent") + "\n" +
		S.Muted.Render("  ↑↓       ") + S.Dim.Render("Select agent from list") + "\n" +
		S.Muted.Render("  ←→ / hl  ") + S.Dim.Render("Switch panel focus") + "\n\n" +

		S.Blue.Render("Sessions  ([s])") + "\n" +
		S.Muted.Render("  enter    ") + S.Dim.Render("Load session into sim") + "\n" +
		S.Muted.Render("  n        ") + S.Dim.Render("Save current config as session") + "\n" +
		S.Muted.Render("  f        ") + S.Dim.Render("Fork selected session") + "\n" +
		S.Muted.Render("  d        ") + S.Dim.Render("Delete selected session") + "\n\n" +

		S.Blue.Render("History / Query  ([q])") + "\n" +
		S.Muted.Render("  tab      ") + S.Dim.Render("Cycle view: posts/actions/timeline/stats") + "\n" +
		S.Muted.Render("  r        ") + S.Dim.Render("Refresh data") + "\n" +
		S.Muted.Render("  ↑↓ / jk  ") + S.Dim.Render("Scroll content") + "\n\n" +

		S.Dim.Render("[esc] or [?] to close help")

	box := boxStyleBlue.Width(min(70, w-4)).Render(content)

	// Center the box
	boxW := lipgloss.Width(box)
	padL := (w - boxW) / 2
	if padL < 0 {
		padL = 0
	}
	padT := (h - strings.Count(content, "\n") - 4) / 2
	if padT < 0 {
		padT = 1
	}

	var lines []string
	for i := 0; i < padT; i++ {
		lines = append(lines, "")
	}
	lines = append(lines, strings.Repeat(" ", padL)+box)
	return strings.Join(lines, "\n")
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}


// ─── Wizard View ──────────────────────────────────────────────────────────────

func (m App) viewWizard() string {
	w := m.width
	if w < 10 {
		return "Initializing…"
	}

	art := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colBlue)).Render(
		"╔═══════════════════════════════════════════╗\n" +
			"║                 FISHNET                   ║\n" +
			"╚═══════════════════════════════════════════╝")
	sub := S.Dim.Render("    GraphRAG + Social Simulation CLI")
	welcome := S.Muted.Render("  Welcome! Let's set up your first project.")

	var formLines []string
	formLines = append(formLines, "")

	switch m.wizardStep {
	case 0:
		formLines = append(formLines,
			S.Blue.Render("  Project Name:   ")+m.wizardName.View())
		formLines = append(formLines, "")
		formLines = append(formLines,
			S.Dim.Render("  Docs Directory: ")+"(next)")
	case 1:
		formLines = append(formLines,
			S.Muted.Render("  Project Name:   ")+S.Green.Render(m.wizardName.Value()))
		formLines = append(formLines, "")
		formLines = append(formLines,
			S.Blue.Render("  Docs Directory: ")+m.wizardDir.View())
	case 2:
		formLines = append(formLines,
			S.Muted.Render("  Project Name:   ")+S.Green.Render(m.wizardName.Value()))
		formLines = append(formLines, "")
		formLines = append(formLines,
			S.Muted.Render("  Docs Directory: ")+S.Green.Render(m.wizardDir.Value()))
		formLines = append(formLines, "")
		formLines = append(formLines,
			"  "+m.spinner.View()+" "+S.Yellow.Render("Initializing project…"))
		if m.wizardMsg != "" {
			formLines = append(formLines, "")
			formLines = append(formLines, "  "+S.Dim.Render(m.wizardMsg))
		}
	}

	formLines = append(formLines, "")
	if m.wizardStep < 2 {
		formLines = append(formLines, S.Dim.Render("  [enter] Next  [ctrl+c] Quit"))
	}

	form := strings.Join(formLines, "\n")
	content := art + "\n" + sub + "\n\n" + welcome + "\n" + form

	lineCount := strings.Count(content, "\n") + 1
	padT := (m.height - lineCount) / 2
	if padT < 0 {
		padT = 0
	}

	contentW := 48
	padL := (w - contentW) / 2
	if padL < 2 {
		padL = 2
	}
	pad := strings.Repeat(" ", padL)

	var out []string
	for i := 0; i < padT; i++ {
		out = append(out, "")
	}
	for _, line := range strings.Split(content, "\n") {
		out = append(out, pad+line)
	}
	return strings.Join(out, "\n")
}

// ─── Session View ─────────────────────────────────────────────────────────────

func (m App) viewSession() string {
	w := m.width - 4
	heading := S.Bold.Render("Sessions")

	panelH := m.height - 8
	if panelH < 4 {
		panelH = 4
	}
	leftW := (w * 40) / 100
	rightW := w - leftW - 3

	var listLines []string
	if len(m.sessionList) == 0 {
		listLines = append(listLines, S.Dim.Render("  No sessions saved yet."))
		listLines = append(listLines, S.Dim.Render("  Press [n] to save current sim config."))
	} else {
		listH := panelH - 4
		start := m.sessionIdx - listH/2
		if start < 0 {
			start = 0
		}
		end := start + listH
		if end > len(m.sessionList) {
			end = len(m.sessionList)
		}
		for i := start; i < end; i++ {
			s := m.sessionList[i]
			name := s.Name
			if name == "" {
				name = s.ID
			}
			created := s.CreatedAt.Format("01/02 15:04")
			line := fmt.Sprintf("  %-20s  %s  %s",
				clip(name, 20), created, clip(s.Scenario, leftW-34))
			if i == m.sessionIdx {
				listLines = append(listLines, selectedStyle.Width(leftW-2).Render(line))
			} else {
				listLines = append(listLines, S.Muted.Render(line))
			}
		}
	}
	listLines = append(listLines, "")
	listLines = append(listLines, S.Dim.Render("  [n] save current  [f] fork  [d] delete"))

	leftContent := strings.Join(listLines, "\n")
	leftPanel := leftPanelActiveStyle.Width(leftW).Height(panelH).Render(
		S.Blue.Render("Saved Sessions") + "\n" + leftContent)

	var rightContent string
	if m.sessionIdx < len(m.sessionList) {
		s := m.sessionList[m.sessionIdx]
		name := s.Name
		if name == "" {
			name = s.ID
		}
		platforms := strings.Join(s.Platforms, ", ")
		if platforms == "" {
			platforms = "(none)"
		}
		tags := strings.Join(s.Tags, ", ")
		if tags == "" {
			tags = "(none)"
		}
		rightContent = "\n" +
			"  " + S.Blue.Render(name) + "\n" +
			"  " + S.Dim.Render("id: "+s.ID) + "\n\n" +
			"  " + S.Muted.Render("Scenario:  ") + S.White.Render(clip(s.Scenario, rightW-14)) + "\n" +
			"  " + S.Muted.Render("Rounds:    ") + S.Bold.Render(fmt.Sprint(s.Rounds)) + "\n" +
			"  " + S.Muted.Render("Agents:    ") + S.Bold.Render(fmt.Sprint(s.MaxAgents)) + "\n" +
			"  " + S.Muted.Render("Platforms: ") + S.Blue.Render(platforms) + "\n" +
			"  " + S.Muted.Render("Tags:      ") + S.Dim.Render(tags) + "\n" +
			"  " + S.Muted.Render("SimID:     ") + S.Dim.Render(s.SimID) + "\n\n" +
			"  " + S.Green.Render("[enter]") + " load into sim\n" +
			"  " + S.Dim.Render("[f] fork  [d] delete")
	} else {
		rightContent = "\n  " + S.Dim.Render("Select a session to view details.")
	}

	rightPanel := rightPanelStyle.Width(rightW).Height(panelH).Render(
		S.Muted.Render("Detail") + "\n" + rightContent)

	splitRow := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, " ", rightPanel)
	return "\n  " + heading + "\n\n  " + splitRow
}

// ─── Query / History View ─────────────────────────────────────────────────────

func (m App) viewQuery() string {
	w := m.width - 4
	heading := S.Bold.Render("Query / History")

	tabNames := []string{"Posts", "Actions", "Timeline", "Stats"}
	var tabParts []string
	for i, t := range tabNames {
		if i == m.queryTab {
			tabParts = append(tabParts, tabActiveStyle.Render(" "+t+" "))
		} else {
			tabParts = append(tabParts, tabInactiveStyle.Render(" "+t+" "))
		}
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabParts...)

	panelH := m.height - 10
	if panelH < 5 {
		panelH = 5
	}

	var displayLines []string
	if len(m.queryLines) == 0 {
		if m.projectID == "" {
			displayLines = append(displayLines, S.Dim.Render("  No project loaded."))
		} else {
			displayLines = append(displayLines, S.Dim.Render("  No data. Press [r] to refresh."))
		}
	} else {
		start := m.queryIdx
		end := start + panelH
		if end > len(m.queryLines) {
			end = len(m.queryLines)
		}
		displayLines = m.queryLines[start:end]
	}

	content := strings.Join(displayLines, "\n")
	panel := boxStyle.Width(w).Render(content)

	return "\n  " + heading + "\n\n  " + tabBar + "\n\n  " + panel
}

// ─── Wizard Key Handler ───────────────────────────────────────────────────────

func (m *App) handleWizardKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit
	case "enter":
		switch m.wizardStep {
		case 0:
			if strings.TrimSpace(m.wizardName.Value()) == "" {
				return nil
			}
			m.wizardStep = 1
			m.wizardName.Blur()
			m.wizardDir.Focus()
		case 1:
			dir := strings.TrimSpace(m.wizardDir.Value())
			if dir == "" {
				dir = "."
			}
			m.wizardDir.SetValue(dir)
			m.wizardStep = 2
			m.wizardDir.Blur()
			return m.runWizard()
		}
	}
	return nil
}

// ─── Mouse Click Handler ──────────────────────────────────────────────────────

// handleMouseClick processes left-button click events.
// Row 0 = header, row 1 = tabs bar (approximate).
func (m *App) handleMouseClick(x, y int) tea.Cmd {
	// Skip clicks while wizard or help is active.
	if m.screen == screenWizard || m.screen == screenHelp {
		return nil
	}

	// Tab bar is rendered at row 1 (0-indexed: header=0, tabs=1).
	if y == 1 {
		// Walk tab labels to find which one was clicked.
		tabScreens := []screen{
			screenGraph, screenEnv, screenSim, screenReport, screenChat,
			screenSession, screenQuery,
		}
		col := 0
		for i, label := range tabLabels {
			// Each tab is rendered with a 1-space pad on each side inside the style.
			// Use the raw label width + 2 (for the style padding) as an approximation.
			labelW := lipgloss.Width(label) + 2
			if x >= col && x < col+labelW {
				if i < len(tabScreens) {
					m.screen = tabScreens[i]
					m.simInput.Blur()
					m.chatInput.Blur()
					if m.screen == screenSim && !m.simRunning {
						m.simInput.Focus()
					}
					if m.screen == screenChat && m.panelFocus == focusRight {
						m.chatInput.Focus()
					}
				}
				return nil
			}
			col += labelW
		}
		return nil
	}

	// In split-panel views, clicking the left or right half sets panelFocus.
	switch m.screen {
	case screenEnv, screenChat, screenReport:
		half := m.width / 2
		if x < half {
			m.panelFocus = focusLeft
			m.chatInput.Blur()
		} else {
			m.panelFocus = focusRight
			if m.screen == screenChat {
				m.chatInput.Focus()
			}
		}
	}
	return nil
}

// wizardDoneMsg signals wizard completion.
type wizardDoneMsg struct {
	projectID string
	err       error
}

func (m *App) runWizard() tea.Cmd {
	name := strings.TrimSpace(m.wizardName.Value())
	dir := strings.TrimSpace(m.wizardDir.Value())
	if dir == "" {
		dir = "."
	}

	database := m.db
	cfg := m.cfg

	apiKey := cfg.LLM.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}
	cfg.LLM.APIKey = apiKey

	// Create a buffered channel for incremental progress messages.
	ch := make(chan tea.Msg, 16)
	m.wizardCh = ch

	go func() {
		defer close(ch)
		ctx := context.Background()

		ch <- wizardProgressMsg{stage: "Creating project…"}

		projectID, err := database.UpsertProject(name, dir)
		if err != nil {
			ch <- wizardDoneMsg{err: fmt.Errorf("create project: %w", err)}
			return
		}

		ch <- wizardProgressMsg{stage: "Reading documents…"}

		docs, err := doc.ReadDir(dir)
		if err != nil {
			ch <- wizardDoneMsg{projectID: projectID, err: fmt.Errorf("read docs: %w", err)}
			return
		}

		ch <- wizardProgressMsg{stage: fmt.Sprintf("Chunking and storing %d documents…", len(docs))}

		for _, d := range docs {
			chunks := doc.Chunk(d.Content, cfg.Graph.ChunkSize, cfg.Graph.ChunkOverlap)
			docID, dErr := database.AddDocument(projectID, d.Path, d.Name, d.Content, len(chunks))
			if dErr != nil {
				continue
			}
			for i, c := range chunks {
				database.AddChunk(docID, projectID, c, i)
			}
		}

		unproc, err := database.UnprocessedChunks(projectID)
		if err != nil {
			ch <- wizardDoneMsg{projectID: projectID, err: fmt.Errorf("chunks: %w", err)}
			return
		}

		if len(unproc) > 0 {
			ch <- wizardProgressMsg{stage: fmt.Sprintf("Building knowledge graph (%d chunks)…", len(unproc))}
			client := llm.New(cfg.LLM)
			builder := graph.NewBuilder(database, client)
			_, err = builder.BuildFromChunks(ctx, projectID, unproc, cfg.LLM.MaxConcurrency, nil)
			if err != nil {
				ch <- wizardDoneMsg{projectID: projectID, err: fmt.Errorf("graph build: %w", err)}
				return
			}
		}

		ch <- wizardDoneMsg{projectID: projectID}
	}()

	// Return a tick so the channel starts being polled immediately.
	return tickEvery(100 * time.Millisecond)
}

// drainWizardCh reads one message from the wizard progress channel (non-blocking).
func (m *App) drainWizardCh() tea.Cmd {
	ch := m.wizardCh
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			return msg
		default:
			return nil
		}
	}
}

// ─── Session & Query Async Commands ──────────────────────────────────────────

// loadSessionsMsg carries loaded sessions back to the Update loop.
type loadSessionsMsg struct {
	sessions []*session.Session
	err      error
}

// queryDataMsg carries query result lines.
type queryDataMsg struct {
	lines []string
	tab   int
}

func (m *App) loadSessions() tea.Cmd {
	return func() tea.Msg {
		mgr := session.NewManager(".")
		sessions, err := mgr.List()
		return loadSessionsMsg{sessions: sessions, err: err}
	}
}

func (m *App) saveCurrentSession() tea.Cmd {
	name := fmt.Sprintf("session-%s", time.Now().Format("20060102-150405"))
	scenario := m.simScenario
	rounds := m.simRounds
	maxAgents := m.simMaxAgents
	platforms := append([]string{}, m.simPlatforms...)

	return func() tea.Msg {
		mgr := session.NewManager(".")
		s := &session.Session{
			Name:      name,
			Scenario:  scenario,
			Rounds:    rounds,
			MaxAgents: maxAgents,
			Platforms: platforms,
		}
		if err := mgr.Save(s); err != nil {
			return logMsg{"✗ save session: " + err.Error()}
		}
		sessions, _ := mgr.List()
		return loadSessionsMsg{sessions: sessions}
	}
}

func (m *App) forkSession(id string) tea.Cmd {
	newName := fmt.Sprintf("fork-%s", time.Now().Format("150405"))
	return func() tea.Msg {
		mgr := session.NewManager(".")
		_, err := mgr.Fork(id, newName)
		if err != nil {
			return logMsg{"✗ fork session: " + err.Error()}
		}
		sessions, _ := mgr.List()
		return loadSessionsMsg{sessions: sessions}
	}
}

func (m *App) deleteSession(id string) tea.Cmd {
	return func() tea.Msg {
		mgr := session.NewManager(".")
		if err := mgr.Delete(id); err != nil {
			return logMsg{"✗ delete session: " + err.Error()}
		}
		sessions, _ := mgr.List()
		return loadSessionsMsg{sessions: sessions}
	}
}

func (m *App) loadQueryData() tea.Cmd {
	tab := m.queryTab
	projectID := m.projectID
	database := m.db

	simID := ""
	if database != nil && projectID != "" {
		sims, _ := database.GetSimsByProject(projectID, 1)
		if len(sims) > 0 {
			simID = sims[0].ID
		}
	}

	return func() tea.Msg {
		var lines []string
		if projectID == "" || database == nil {
			return queryDataMsg{lines: []string{"  No project loaded."}, tab: tab}
		}

		switch tab {
		case 0: // Posts
			if simID == "" {
				lines = append(lines, "  No simulation data yet.")
				break
			}
			posts, err := database.GetSimPosts(simID, "", 50)
			if err != nil {
				lines = append(lines, "  Error: "+err.Error())
				break
			}
			lines = append(lines, fmt.Sprintf("  Posts (%d) — sim %s", len(posts), simID))
			lines = append(lines, "")
			for _, p := range posts {
				tag := "[" + p.Platform + "]"
				author := clip(p.AuthorName, 16)
				content := clip(p.Content, 80)
				round := fmt.Sprintf("r%d", p.Round)
				lines = append(lines, fmt.Sprintf("  %-6s %-18s %-4s  %s", tag, author, round, content))
			}

		case 1: // Actions
			if simID == "" {
				lines = append(lines, "  No simulation data yet.")
				break
			}
			actions, err := database.GetSimActions(simID, "", "", 50)
			if err != nil {
				lines = append(lines, "  Error: "+err.Error())
				break
			}
			lines = append(lines, fmt.Sprintf("  Actions (%d) — sim %s", len(actions), simID))
			lines = append(lines, "")
			for _, a := range actions {
				tag := "[" + a.Platform + "]"
				agent := clip(a.AgentName, 16)
				content := a.ActionType
				if a.Content != "" {
					content += " " + clip(a.Content, 60)
				}
				lines = append(lines, fmt.Sprintf("  %-6s %-18s  %s", tag, agent, content))
			}

		case 2: // Timeline
			if simID == "" {
				lines = append(lines, "  No simulation data yet.")
				break
			}
			timeline, err := database.GetSimTimeline(simID, 50)
			if err != nil {
				lines = append(lines, "  Error: "+err.Error())
				break
			}
			lines = append(lines, fmt.Sprintf("  Timeline (%d events) — sim %s", len(timeline), simID))
			lines = append(lines, "")
			for _, a := range timeline {
				tag := "[" + a.Platform + "]"
				agent := clip(a.AgentName, 16)
				content := a.ActionType
				if a.Content != "" {
					content = clip(a.Content, 60)
				}
				round := fmt.Sprintf("r%02d", a.Round)
				lines = append(lines, fmt.Sprintf("  %-6s %s %-18s  %s", tag, round, agent, content))
			}

		case 3: // Stats
			if simID == "" {
				lines = append(lines, "  No simulation data yet.")
				break
			}
			stats, err := database.GetAgentStats(simID)
			if err != nil {
				lines = append(lines, "  Error: "+err.Error())
				break
			}
			lines = append(lines, fmt.Sprintf("  Agent Stats (%d agents) — sim %s", len(stats), simID))
			lines = append(lines, "")
			lines = append(lines, fmt.Sprintf("  %-20s %6s %6s %6s %6s", "Agent", "Posts", "Likes", "RT", "Cmts"))
			lines = append(lines, "  "+strings.Repeat("─", 50))
			for _, st := range stats {
				lines = append(lines, fmt.Sprintf(
					"  %-20s %6d %6d %6d %6d",
					clip(st.AgentName, 20),
					st.TotalPosts, st.TotalLikes, st.TotalReposts, st.TotalComments))
			}
		}

		return queryDataMsg{lines: lines, tab: tab}
	}
}

// ─── Async Commands ───────────────────────────────────────────────────────────

func (m *App) startAnalyze() tea.Cmd {
	m.analyzeRunning = true
	m.addLog("→ Analyzing " + m.analyzeDir + "…")

	cfg := m.cfg
	database := m.db
	projectID := m.projectID
	dir := m.analyzeDir

	return func() tea.Msg {
		ctx := context.Background()

		// Read documents
		docs, err := doc.ReadDir(dir)
		if err != nil {
			return analyzeProgressMsg{err: err, final: true}
		}
		if len(docs) == 0 {
			return analyzeProgressMsg{err: fmt.Errorf("no documents found in %s", dir), final: true}
		}

		// Chunk and store
		for _, d := range docs {
			chunks := doc.Chunk(d.Content, cfg.Graph.ChunkSize, cfg.Graph.ChunkOverlap)
			docID, err := database.AddDocument(projectID, d.Path, d.Name, d.Content, len(chunks))
			if err != nil {
				continue
			}
			for i, c := range chunks {
				database.AddChunk(docID, projectID, c, i)
			}
		}

		// Extract graph
		chunks, err := database.UnprocessedChunks(projectID)
		if err != nil {
			return analyzeProgressMsg{err: err, final: true}
		}
		if len(chunks) == 0 {
			return analyzeProgressMsg{final: true}
		}

		apiKey := cfg.LLM.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("ANTHROPIC_API_KEY")
			}
		}
		cfg.LLM.APIKey = apiKey

		client := llm.New(cfg.LLM)
		builder := graph.NewBuilder(database, client)

		prog, err := builder.BuildFromChunks(
			ctx, projectID, chunks, cfg.LLM.MaxConcurrency,
			func(p graph.Progress) {
				_ = p // progress handled on next tick
			})
		if err != nil {
			return analyzeProgressMsg{err: err, final: true}
		}
		return analyzeProgressMsg{
			done: prog.Done, total: int64(prog.Total),
			nodes: prog.NodesAdded, edges: prog.EdgesAdded,
			final: true,
		}
	}
}

func (m *App) startSim() tea.Cmd {
	m.simRunning = true
	m.simDone = false
	m.simRound = 0
	m.simActions = nil
	m.simStartTime = time.Now()
	m.addLog("→ Simulation: " + clip(m.simScenario, 50))

	ch := make(chan sim.RoundProgress, 200)
	m.simCh = ch

	cfg := m.cfg
	database := m.db
	projectID := m.projectID
	scenario := m.simScenario
	rounds := m.simRounds
	agents := m.simMaxAgents
	platforms := m.simPlatforms

	apiKey := cfg.LLM.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}
	cfg.LLM.APIKey = apiKey

	go func() {
		client := llm.New(cfg.LLM)
		ps := sim.NewPlatformSim(database, client)
		err := ps.Run(context.Background(), projectID, sim.RoundConfig{
			Scenario:  scenario,
			MaxRounds: rounds,
			MaxAgents: agents,
			Platforms: platforms,
			OutputDir: ".fishnet/simulations",
		}, ch)
		close(ch)
		_ = err
	}()

	return tickEvery(100 * time.Millisecond)
}

func (m *App) drainSimCh() tea.Cmd {
	ch := m.simCh
	if ch == nil {
		return tickEvery(200 * time.Millisecond)
	}

	return func() tea.Msg {
		// Non-blocking drain: read up to 5 events
		for i := 0; i < 5; i++ {
			select {
			case prog, ok := <-ch:
				if !ok {
					return simDoneMsg{}
				}
				if prog.Done {
					return simDoneMsg{}
				}
				return simProgressMsg{prog: prog}
			default:
				return tickEvery(100 * time.Millisecond)()
			}
		}
		return tickEvery(100 * time.Millisecond)()
	}
}

func (m *App) startReport() tea.Cmd {
	m.reportRunning = true
	m.reportSections = nil
	m.reportContent = ""
	m.reportSectionIdx = 0
	m.addLog("→ Generating report…")

	cfg := m.cfg
	database := m.db
	projectID := m.projectID
	scenario := m.simScenario

	apiKey := cfg.LLM.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}
	cfg.LLM.APIKey = apiKey

	sectionCh := make(chan report.Section, 10)
	doneCh := make(chan error, 1)

	go func() {
		client := llm.New(cfg.LLM)
		agent := report.New(database, client)
		_, err := agent.Generate(context.Background(), projectID, scenario, func(s report.Section) {
			sectionCh <- s
		})
		doneCh <- err
		close(sectionCh)
	}()

	return func() tea.Msg {
		for s := range sectionCh {
			return reportSectionMsg{section: s}
		}
		err := <-doneCh
		return reportSectionMsg{done: true, err: err}
	}
}

func (m *App) doInterview(question string) tea.Cmd {
	cfg := m.cfg
	database := m.db
	projectID := m.projectID
	target := m.chatTarget
	history := append([]llm.Message{}, m.chatHistory...)

	apiKey := cfg.LLM.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}
	cfg.LLM.APIKey = apiKey

	return func() tea.Msg {
		client := llm.New(cfg.LLM)
		agent := report.New(database, client)
		resp, histMsg, err := agent.Interview(context.Background(), projectID, target, question, history)
		if err != nil {
			return interviewRespMsg{err: err}
		}
		_ = histMsg
		return interviewRespMsg{resp: resp}
	}
}

func (m *App) openWebViz() tea.Cmd {
	database := m.db
	projectID := m.projectID
	return func() tea.Msg {
		url, err := viz.Serve(database, projectID)
		if err != nil {
			return logMsg{"✗ viz: " + err.Error()}
		}
		openBrowser(url)
		return logMsg{"✓ Web viz: " + url}
	}
}

func (m *App) runCommunity() tea.Cmd {
	cfg := m.cfg
	database := m.db
	projectID := m.projectID
	m.addLog("→ Community detection…")

	apiKey := cfg.LLM.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("ANTHROPIC_API_KEY")
		}
	}
	cfg.LLM.APIKey = apiKey

	return func() tea.Msg {
		client := llm.New(cfg.LLM)
		results, err := graph.RunCommunityDetection(
			context.Background(), database, client, projectID, cfg.Graph.CommunityMinSize)
		if err != nil {
			return logMsg{"✗ Community: " + err.Error()}
		}
		return logMsg{fmt.Sprintf("✓ %d communities detected", len(results))}
	}
}

func (m *App) saveReport() {
	if m.reportContent == "" {
		return
	}
	if err := os.WriteFile("report.md", []byte(m.reportContent), 0644); err == nil {
		m.addLog("✓ Saved report.md")
	}
}


// ─── Debug Screen ──────────────────────────────────────────────────────────────

func (m *App) buildDebugInfo() {
	var lines []string
	lines = append(lines, "── Config ──────────────────────────────")
	lines = append(lines, fmt.Sprintf("  Provider:   %s", m.cfg.LLM.Provider))
	lines = append(lines, fmt.Sprintf("  Model:      %s", m.cfg.LLM.Model))
	keyMasked := "(not set)"
	if m.cfg.LLM.APIKey != "" {
		k := m.cfg.LLM.APIKey
		if len(k) > 8 {
			keyMasked = k[:4] + "…" + k[len(k)-4:]
		} else {
			keyMasked = "****"
		}
	}
	lines = append(lines, fmt.Sprintf("  API Key:    %s", keyMasked))
	lines = append(lines, fmt.Sprintf("  Base URL:   %s", m.cfg.LLM.BaseURL))
	lines = append(lines, fmt.Sprintf("  LLM Status: %v (%s)", m.llmStatusOK, m.llmStatusMsg))
	lines = append(lines, "")
	lines = append(lines, "── Project ─────────────────────────────")
	lines = append(lines, fmt.Sprintf("  ProjectID:  %s", m.projectID))
	lines = append(lines, fmt.Sprintf("  Nodes:      %d", len(m.graphNodes)))
	lines = append(lines, fmt.Sprintf("  Edges:      %d", len(m.graphEdges)))
	lines = append(lines, "")
	lines = append(lines, "── Logs (recent) ───────────────────────")
	start := len(m.logs) - 20
	if start < 0 {
		start = 0
	}
	for _, l := range m.logs[start:] {
		lines = append(lines, "  "+l)
	}
	m.debugLines = lines
}

func (m App) viewDebug() string {
	heading := S.Yellow.Render("Debug Info") + S.Dim.Render("  [D] close")
	w := m.width - 4
	panelH := m.height - 8
	if panelH < 5 {
		panelH = 5
	}
	start := 0
	end := len(m.debugLines)
	if end-start > panelH {
		end = start + panelH
	}
	var visible []string
	if end <= len(m.debugLines) {
		visible = m.debugLines[start:end]
	}
	content := strings.Join(visible, "\n")
	panel := boxStyleOrange.Width(w).Render(content)
	return "\n  " + heading + "\n\n  " + panel
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func (m *App) addLog(s string) {
	m.logs = append(m.logs, s)
	if len(m.logs) > m.maxLogs {
		m.logs = m.logs[len(m.logs)-m.maxLogs:]
	}
}

func (m App) renderLogs(n int) string {
	if len(m.logs) == 0 {
		return S.Dim.Render("  No activity yet")
	}
	start := len(m.logs) - n
	if start < 0 {
		start = 0
	}
	var lines []string
	for _, l := range m.logs[start:] {
		lines = append(lines, "  "+S.Dim.Render(l))
	}
	return strings.Join(lines, "\n")
}

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return t })
}

func openBrowser(url string) {
	// platform-specific open handled by viz package
	_ = url
}
