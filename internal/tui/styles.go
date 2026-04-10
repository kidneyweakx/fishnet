package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ─── Palette ─────────────────────────────────────────────────────────────────

const (
	colBg     = "#0f1117"
	colPanel  = "#1a1d2e"
	colBorder = "#4a5568" // was #2d3748 — too dark, borders invisible
	colText   = "#e2e8f0"
	colDim    = "#718096" // was #4a5568 — too dark, text invisible
	colMuted  = "#a0aec0" // was #718096 — bumped for readability
	colBlue   = "#63b3ed"
	colGreen  = "#68d391"
	colYellow = "#f6e05e"
	colRed    = "#fc8181"
	colPurple = "#b794f4"
	colOrange = "#f6ad55"
	colTeal   = "#76e4f7"
	colCyan   = "#4fd1c5"
)

// ─── Base Styles ─────────────────────────────────────────────────────────────

var (
	S = struct {
		Bold   lipgloss.Style
		Dim    lipgloss.Style
		Muted  lipgloss.Style
		Blue   lipgloss.Style
		Green  lipgloss.Style
		Yellow lipgloss.Style
		Red    lipgloss.Style
		Purple lipgloss.Style
		Teal   lipgloss.Style
		Orange lipgloss.Style
		Cyan   lipgloss.Style
		White  lipgloss.Style
	}{
		Bold:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colText)),
		Dim:    lipgloss.NewStyle().Foreground(lipgloss.Color(colDim)),
		Muted:  lipgloss.NewStyle().Foreground(lipgloss.Color(colMuted)),
		Blue:   lipgloss.NewStyle().Foreground(lipgloss.Color(colBlue)),
		Green:  lipgloss.NewStyle().Foreground(lipgloss.Color(colGreen)),
		Yellow: lipgloss.NewStyle().Foreground(lipgloss.Color(colYellow)),
		Red:    lipgloss.NewStyle().Foreground(lipgloss.Color(colRed)),
		Purple: lipgloss.NewStyle().Foreground(lipgloss.Color(colPurple)),
		Teal:   lipgloss.NewStyle().Foreground(lipgloss.Color(colTeal)),
		Orange: lipgloss.NewStyle().Foreground(lipgloss.Color(colOrange)),
		Cyan:   lipgloss.NewStyle().Foreground(lipgloss.Color(colCyan)),
		White:  lipgloss.NewStyle().Foreground(lipgloss.Color(colText)),
	}

	// Container styles
	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colBorder)).
			Padding(0, 1)

	boxStyleBlue = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colBlue)).
			Padding(0, 1)

	boxStyleGreen = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colGreen)).
			Padding(0, 1)

	boxStyleTeal = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colTeal)).
			Padding(0, 1)

	boxStyleOrange = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colOrange)).
			Padding(0, 1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colBlue)).
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(colBorder)).
			PaddingBottom(0)

	tabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(colText)).
			Background(lipgloss.Color(colBlue)).
			Padding(0, 2)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(colDim)).
				Padding(0, 2)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colMuted)).
			BorderTop(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(colBorder))

	inputLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colMuted))

	// Platform-specific
	twitterColor = lipgloss.NewStyle().Foreground(lipgloss.Color(colTeal))
	redditColor  = lipgloss.NewStyle().Foreground(lipgloss.Color(colOrange))

	// Table/list
	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color(colBlue))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(colText)).
			Background(lipgloss.Color("#243656"))

	// Panel split helpers
	leftPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colBorder)).
			Padding(0, 1)

	leftPanelActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color(colBlue)).
				Padding(0, 1)

	rightPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(colBorder)).
			Padding(0, 1)

	rightPanelActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color(colBlue)).
				Padding(0, 1)

	// Section list item styles
	sectionDoneStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(colGreen))
	sectionActiveStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colYellow))
	sectionPendingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colDim))
)

// ─── Helper Renderers ─────────────────────────────────────────────────────────

func progressBar(pct, width int) string {
	filled := pct * width / 100
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	var bar string
	for i := 0; i < width; i++ {
		if i < filled {
			bar += S.Green.Render("█")
		} else {
			bar += S.Dim.Render("░")
		}
	}
	return bar
}

// traitBar renders a labeled progress bar for personality traits (0.0–1.0).
func traitBar(label string, val float64, barWidth int) string {
	if val < 0 {
		val = 0
	}
	if val > 1 {
		val = 1
	}
	pct := int(val * 100)
	bar := progressBar(pct, barWidth)
	return fmt.Sprintf("  %-12s %s  %.2f", label+":", bar, val)
}

func actionIcon(typ string) string {
	switch typ {
	case "CREATE_POST":
		return S.Green.Render("✎")
	case "CREATE_COMMENT":
		return S.Muted.Render("💬")
	case "LIKE_POST":
		return S.Red.Render("♥")
	case "DISLIKE_POST":
		return S.Red.Render("↓")
	case "LIKE_COMMENT":
		return S.Red.Render("⇡")
	case "DISLIKE_COMMENT":
		return S.Red.Render("⇣")
	case "REPOST":
		return S.Teal.Render("↺")
	case "QUOTE_POST":
		return S.Purple.Render("❝")
	case "FOLLOW":
		return S.Blue.Render("+")
	case "MUTE":
		return S.Dim.Render("⊘")
	case "SEARCH_POSTS":
		return S.Blue.Render("🔍")
	case "SEARCH_USER":
		return S.Blue.Render("👤")
	case "TREND":
		return S.Teal.Render("📈")
	case "REFRESH":
		return S.Dim.Render("⟳")
	case "DO_NOTHING":
		return S.Dim.Render("·")
	default:
		return S.Dim.Render("·")
	}
}

func clip(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// clipLines clips a multi-line string to at most maxLines newline-separated lines.
func clipLines(s string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n")
}

// stanceStyle returns a colored string for agent stance.
func stanceStyle(stance string) string {
	switch strings.ToLower(stance) {
	case "supportive", "pro", "positive", "agree":
		return S.Green.Render(stance)
	case "opposing", "against", "negative", "disagree":
		return S.Red.Render(stance)
	default:
		return S.Yellow.Render(stance)
	}
}

// elapsedStr formats a duration as HH:MM:SS.
func elapsedStr(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
