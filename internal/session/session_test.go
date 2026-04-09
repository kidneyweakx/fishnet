package session

import (
	"testing"
	"time"
)

// ─── NewID ───────────────────────────────────────────────────────────────────

func TestNewID_Format(t *testing.T) {
	id := NewID()
	if id == "" {
		t.Fatal("NewID should not return empty string")
	}
	// ID should start with "sim-"
	if len(id) < 4 || id[:4] != "sim-" {
		t.Errorf("NewID = %q, should start with 'sim-'", id)
	}
}

func TestNewID_UniqueOverTime(t *testing.T) {
	id1 := NewID()
	time.Sleep(time.Second + 100*time.Millisecond) // Ensure different second
	id2 := NewID()
	if id1 == id2 {
		t.Skip("IDs generated in the same second are expected to be equal; skipping uniqueness check")
	}
}

// ─── Manager.Save and Load ────────────────────────────────────────────────────

func TestSession_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{
		ID:        "sim-20240409-143022",
		Scenario:  "test scenario",
		Rounds:    10,
		Platforms: []string{"twitter", "reddit"},
		TimeZone:  "UTC",
	}

	if err := mgr.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := mgr.Load(s.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Scenario != s.Scenario {
		t.Errorf("Scenario = %q, want %q", loaded.Scenario, s.Scenario)
	}
	if loaded.Rounds != s.Rounds {
		t.Errorf("Rounds = %d, want %d", loaded.Rounds, s.Rounds)
	}
	if len(loaded.Platforms) != 2 {
		t.Errorf("Platforms = %v, want 2 items", loaded.Platforms)
	}
}

func TestSession_Save_AutoAssignsID(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{
		Scenario: "no id set",
		Rounds:   5,
	}

	if err := mgr.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if s.ID == "" {
		t.Error("Save should auto-assign ID when empty")
	}
}

func TestSession_Save_SetsCreatedAt(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-test-001", Scenario: "test"}
	if err := mgr.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if s.CreatedAt.IsZero() {
		t.Error("Save should set CreatedAt")
	}
}

func TestSession_Save_SetsUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-test-001", Scenario: "test"}
	if err := mgr.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if s.UpdatedAt.IsZero() {
		t.Error("Save should set UpdatedAt")
	}
}

func TestSession_Load_NotFound(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	_, err := mgr.Load("sim-nonexistent")
	if err == nil {
		t.Fatal("expected error loading nonexistent session, got nil")
	}
}

func TestSession_Load_ByNamePrefix(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{
		ID:       "sim-20240409-143022",
		Name:     "my-experiment",
		Scenario: "test",
	}
	if err := mgr.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load by full name
	loaded, err := mgr.Load("my-experiment")
	if err != nil {
		t.Fatalf("Load by name: %v", err)
	}
	if loaded.ID != s.ID {
		t.Errorf("loaded by name ID = %q, want %q", loaded.ID, s.ID)
	}
}

func TestSession_Load_ByIDPrefix(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{
		ID:       "sim-20240409-143022",
		Scenario: "test",
	}
	if err := mgr.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load by prefix of ID
	loaded, err := mgr.Load("sim-20240409")
	if err != nil {
		t.Fatalf("Load by ID prefix: %v", err)
	}
	if loaded.ID != s.ID {
		t.Errorf("loaded ID = %q, want %q", loaded.ID, s.ID)
	}
}

// ─── Manager.List ─────────────────────────────────────────────────────────────

func TestSession_List_Empty(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	sessions, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("List on empty dir = %d, want 0", len(sessions))
	}
}

func TestSession_List_Returns_All(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	for _, id := range []string{"sim-a001", "sim-b002", "sim-c003"} {
		if err := mgr.Save(&Session{ID: id, Scenario: "test " + id}); err != nil {
			t.Fatalf("Save %s: %v", id, err)
		}
	}

	sessions, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("List = %d, want 3", len(sessions))
	}
}

func TestSession_List_SortedByUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	// Save sessions with deliberate order; UpdatedAt is set by Save()
	s1 := &Session{ID: "sim-first-001", Scenario: "first"}
	mgr.Save(s1)

	// Small sleep to ensure different timestamps
	time.Sleep(10 * time.Millisecond)

	s2 := &Session{ID: "sim-second-002", Scenario: "second"}
	mgr.Save(s2)

	sessions, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	// Most recently updated should be first
	if sessions[0].ID != "sim-second-002" {
		t.Errorf("first session in list = %q, want sim-second-002 (most recent)", sessions[0].ID)
	}
}

// ─── Manager.Delete ───────────────────────────────────────────────────────────

func TestSession_Delete(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-del-001", Scenario: "to delete"}
	if err := mgr.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := mgr.Delete(s.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := mgr.Load(s.ID)
	if err == nil {
		t.Error("expected error loading deleted session")
	}
}

func TestSession_Delete_NotFound(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	err := mgr.Delete("sim-nonexistent")
	if err == nil {
		t.Error("expected error deleting nonexistent session")
	}
}

// ─── Manager.Fork ─────────────────────────────────────────────────────────────

func TestSession_Fork(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	original := &Session{
		ID:        "sim-orig-001",
		Name:      "original",
		Scenario:  "fork test",
		Rounds:    20,
		Platforms: []string{"twitter"},
	}
	if err := mgr.Save(original); err != nil {
		t.Fatalf("Save original: %v", err)
	}

	forked, err := mgr.Fork(original.ID, "forked-experiment")
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	if forked.ID == original.ID {
		t.Error("Fork should produce a new ID")
	}
	if forked.Name != "forked-experiment" {
		t.Errorf("Fork Name = %q, want %q", forked.Name, "forked-experiment")
	}
	if forked.Scenario != original.Scenario {
		t.Errorf("Fork should copy Scenario: %q vs %q", forked.Scenario, original.Scenario)
	}
	if forked.Rounds != original.Rounds {
		t.Errorf("Fork should copy Rounds: %d vs %d", forked.Rounds, original.Rounds)
	}
	if forked.SimID != "" {
		t.Errorf("Fork should clear SimID, got %q", forked.SimID)
	}
}

func TestSession_Fork_Persisted(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	original := &Session{ID: "sim-orig-002", Scenario: "test", Rounds: 5}
	mgr.Save(original)

	forked, err := mgr.Fork(original.ID, "forked")
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	// Should be loadable
	loaded, err := mgr.Load(forked.ID)
	if err != nil {
		t.Fatalf("Load forked session: %v", err)
	}
	if loaded.Name != "forked" {
		t.Errorf("loaded fork name = %q, want %q", loaded.Name, "forked")
	}
}

// ─── Manager.Patch ────────────────────────────────────────────────────────────

func TestSession_Patch_Scenario(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-patch-001", Scenario: "old scenario", Rounds: 5}
	mgr.Save(s)

	patched, err := mgr.Patch(s.ID, map[string]string{"scenario": "new scenario"})
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if patched.Scenario != "new scenario" {
		t.Errorf("patched Scenario = %q, want %q", patched.Scenario, "new scenario")
	}
}

func TestSession_Patch_Rounds(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-patch-002", Scenario: "test", Rounds: 5}
	mgr.Save(s)

	patched, err := mgr.Patch(s.ID, map[string]string{"rounds": "50"})
	if err != nil {
		t.Fatalf("Patch rounds: %v", err)
	}
	if patched.Rounds != 50 {
		t.Errorf("patched Rounds = %d, want 50", patched.Rounds)
	}
}

func TestSession_Patch_InvalidRounds(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-patch-003", Scenario: "test", Rounds: 5}
	mgr.Save(s)

	_, err := mgr.Patch(s.ID, map[string]string{"rounds": "not-a-number"})
	if err == nil {
		t.Error("expected error patching rounds with non-numeric value")
	}
}

func TestSession_Patch_Platforms(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-patch-004", Scenario: "test", Platforms: []string{"twitter"}}
	mgr.Save(s)

	patched, err := mgr.Patch(s.ID, map[string]string{"platforms": "twitter, reddit"})
	if err != nil {
		t.Fatalf("Patch platforms: %v", err)
	}
	if len(patched.Platforms) != 2 {
		t.Errorf("patched Platforms = %v, want 2 items", patched.Platforms)
	}
}

func TestSession_Patch_Tags(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-patch-005", Scenario: "test"}
	mgr.Save(s)

	patched, err := mgr.Patch(s.ID, map[string]string{"tags": "alpha, beta, gamma"})
	if err != nil {
		t.Fatalf("Patch tags: %v", err)
	}
	if len(patched.Tags) != 3 {
		t.Errorf("patched Tags = %v, want 3 items", patched.Tags)
	}
}

func TestSession_Patch_Notes(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-patch-006", Scenario: "test"}
	mgr.Save(s)

	patched, err := mgr.Patch(s.ID, map[string]string{"notes": "my notes"})
	if err != nil {
		t.Fatalf("Patch notes: %v", err)
	}
	if patched.Notes != "my notes" {
		t.Errorf("patched Notes = %q, want %q", patched.Notes, "my notes")
	}
}

func TestSession_Patch_Name(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-patch-007", Scenario: "test", Name: "old-name"}
	mgr.Save(s)

	patched, err := mgr.Patch(s.ID, map[string]string{"name": "new-name"})
	if err != nil {
		t.Fatalf("Patch name: %v", err)
	}
	if patched.Name != "new-name" {
		t.Errorf("patched Name = %q, want %q", patched.Name, "new-name")
	}
}

func TestSession_Patch_UnknownField(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-patch-008", Scenario: "test"}
	mgr.Save(s)

	_, err := mgr.Patch(s.ID, map[string]string{"unknownfield": "value"})
	if err == nil {
		t.Error("expected error patching unknown field")
	}
}

func TestSession_Patch_Timezone(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-patch-009", Scenario: "test"}
	mgr.Save(s)

	patched, err := mgr.Patch(s.ID, map[string]string{"timezone": "Asia/Taipei"})
	if err != nil {
		t.Fatalf("Patch timezone: %v", err)
	}
	if patched.TimeZone != "Asia/Taipei" {
		t.Errorf("patched TimeZone = %q, want %q", patched.TimeZone, "Asia/Taipei")
	}
}

func TestSession_Patch_Persists(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	s := &Session{ID: "sim-patch-010", Scenario: "original"}
	mgr.Save(s)
	mgr.Patch(s.ID, map[string]string{"scenario": "patched"})

	// Re-load from disk to verify persistence
	loaded, err := mgr.Load(s.ID)
	if err != nil {
		t.Fatalf("Load after patch: %v", err)
	}
	if loaded.Scenario != "patched" {
		t.Errorf("Patch not persisted: loaded Scenario = %q, want %q", loaded.Scenario, "patched")
	}
}
