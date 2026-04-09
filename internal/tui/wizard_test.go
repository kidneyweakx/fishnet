package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"fishnet/internal/config"
	"fishnet/internal/db"
)

// openMemDB opens an in-memory SQLite database for testing.
func openMemDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("db.Open(:memory:): %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// ─── Screen selection ─────────────────────────────────────────────────────────

func TestNewApp_WizardScreen(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "")

	if m.screen != screenWizard {
		t.Errorf("screen = %v, want screenWizard (%v)", m.screen, screenWizard)
	}
}

func TestNewApp_DashScreen(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	// With a non-empty projectID the app should start on the dashboard
	m := newApp(cfg, database, "some-project-id")

	if m.screen != screenDash {
		t.Errorf("screen = %v, want screenDash (%v)", m.screen, screenDash)
	}
}

// ─── WizardStep advancement ───────────────────────────────────────────────────

func TestWizardStep_EnterWithEmptyName_NoAdvance(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "")

	// wizardName is empty by default; Enter should not advance
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := model.(App)

	if updated.wizardStep != 0 {
		t.Errorf("wizardStep = %d, want 0 (no advance with empty name)", updated.wizardStep)
	}
}

func TestWizardStep_Advance(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "")
	// Ensure we're in wizard at step 0
	if m.wizardStep != 0 {
		t.Fatalf("expected initial wizardStep=0, got %d", m.wizardStep)
	}

	// Set a non-empty name in the wizard name input
	m.wizardName.SetValue("my-project")

	// Send Enter key
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := model.(App)

	if updated.wizardStep != 1 {
		t.Errorf("wizardStep = %d, want 1 after Enter with non-empty name", updated.wizardStep)
	}
}

func TestWizardStep_AdvanceToRunning(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "")
	m.wizardName.SetValue("my-project")

	// Advance to step 1
	model1, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m1 := model1.(App)
	if m1.wizardStep != 1 {
		t.Fatalf("expected wizardStep=1, got %d", m1.wizardStep)
	}

	// At step 1 (dir input), pressing Enter advances to step 2 (running)
	// wizardDir defaults to "" which will be treated as "."
	model2, _ := m1.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 := model2.(App)

	if m2.wizardStep != 2 {
		t.Errorf("wizardStep = %d, want 2 after second Enter", m2.wizardStep)
	}
}

// ─── wizardDoneMsg handling ───────────────────────────────────────────────────

func TestWizardDoneMsg_Success(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "")
	// Start in wizard screen
	if m.screen != screenWizard {
		t.Fatalf("expected screenWizard, got %v", m.screen)
	}

	// Simulate successful wizard completion
	msg := wizardDoneMsg{projectID: "proj-abc-123"}
	model, _ := m.Update(msg)
	updated := model.(App)

	if updated.screen != screenGraph {
		t.Errorf("screen after success = %v, want screenGraph (%v)", updated.screen, screenGraph)
	}
	if updated.projectID != "proj-abc-123" {
		t.Errorf("projectID = %q, want %q", updated.projectID, "proj-abc-123")
	}
}

func TestWizardDoneMsg_Error(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "")
	// Manually advance wizard to step 2 (running state)
	m.wizardStep = 2

	// Simulate a failed wizard
	msg := wizardDoneMsg{err: errors.New("graph build failed")}
	model, _ := m.Update(msg)
	updated := model.(App)

	// On error, wizardStep should go back to 1 so user can retry
	if updated.wizardStep != 1 {
		t.Errorf("wizardStep after error = %d, want 1", updated.wizardStep)
	}
	// wizardMsg should contain the error
	if updated.wizardMsg == "" {
		t.Error("wizardMsg should be set on error")
	}
	// projectID remains empty on a full failure (no projectID provided)
	if updated.projectID != "" {
		t.Errorf("projectID should stay empty on full wizard failure, got %q", updated.projectID)
	}
}

func TestWizardDoneMsg_ErrorWithPartialProject(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "")
	m.wizardStep = 2

	// Some errors occur after the project was already created (partial failure)
	msg := wizardDoneMsg{projectID: "partial-proj-id", err: errors.New("docs read failed")}
	model, _ := m.Update(msg)
	updated := model.(App)

	// wizardStep should still go to 1
	if updated.wizardStep != 1 {
		t.Errorf("wizardStep after partial error = %d, want 1", updated.wizardStep)
	}
	if updated.wizardMsg == "" {
		t.Error("wizardMsg should be set on error")
	}
}

// ─── screenWizard key handling ────────────────────────────────────────────────

func TestWizardKey_CtrlC_Quits(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c should return a Quit command")
	}
	// Execute the command and check it's tea.Quit
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c cmd() = %T, want tea.QuitMsg", msg)
	}
}

// ─── App initial state ────────────────────────────────────────────────────────

func TestNewApp_DefaultSimRounds(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "some-project")

	if m.simRounds != 10 {
		t.Errorf("default simRounds = %d, want 10", m.simRounds)
	}
}

func TestNewApp_DefaultSimMaxAgents(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "some-project")

	if m.simMaxAgents != 25 {
		t.Errorf("default simMaxAgents = %d, want 25", m.simMaxAgents)
	}
}

func TestNewApp_DefaultPlatforms(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "some-project")

	if len(m.simPlatforms) != 2 {
		t.Errorf("default simPlatforms = %v, want [twitter reddit]", m.simPlatforms)
	}
}

func TestNewApp_WizardFocused(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	// No project — wizard should be focused on name input
	m := newApp(cfg, database, "")

	if !m.wizardName.Focused() {
		t.Error("wizardName should be focused when starting in wizard mode")
	}
}

func TestNewApp_ProjectIDStored(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "my-test-project")

	if m.projectID != "my-test-project" {
		t.Errorf("projectID = %q, want %q", m.projectID, "my-test-project")
	}
}

// ─── View smoke test ──────────────────────────────────────────────────────────

func TestApp_View_Wizard(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "")
	// View should not panic even with zero dimensions
	view := m.View()
	if view == "" {
		t.Error("View() should return non-empty string")
	}
}

func TestApp_View_Dash(t *testing.T) {
	cfg := config.Default()
	database := openMemDB(t)

	m := newApp(cfg, database, "some-project")
	view := m.View()
	if view == "" {
		t.Error("View() should return non-empty string for dashboard")
	}
}
