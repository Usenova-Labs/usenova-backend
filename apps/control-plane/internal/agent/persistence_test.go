package agent

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// openTestRegistry opens (and bootstraps) a SQLite registry at path and
// returns a persistent store. The database is closed automatically when the
// test finishes.
func openTestRegistry(t *testing.T, path string) (*AgentStore, *sql.DB) {
	t.Helper()

	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite(%q): %v", path, err)
	}
	t.Cleanup(func() { db.Close() })

	store, err := NewPersistentAgentStore(db)
	if err != nil {
		t.Fatalf("NewPersistentAgentStore: %v", err)
	}
	return store, db
}

func TestOpenSQLite_BootstrapsSchemaIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")

	for i := 0; i < 2; i++ {
		db, err := OpenSQLite(path)
		if err != nil {
			t.Fatalf("open #%d: %v", i+1, err)
		}
		db.Close()
	}
}

func TestPersistentAgentStore_SurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")

	store, _ := openTestRegistry(t, path)

	registered, err := store.RegisterAgent("agent-1", "host-1", "1.2.3", AgentCapabilities{Docker: true})
	if err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}

	// Simulate activity after registration, then a control-plane restart.
	originalRegistration := registered.RegistrationTime
	time.Sleep(5 * time.Millisecond)
	if err := store.UpdateHeartbeat("agent-1", AgentStatus{Uptime: time.Minute, RunningServices: 3}); err != nil {
		t.Fatalf("UpdateHeartbeat: %v", err)
	}

	restarted, _ := openTestRegistry(t, path)

	got, err := restarted.GetAgent("agent-1")
	if err != nil {
		t.Fatalf("GetAgent after restart: %v", err)
	}

	if got.Hostname != "host-1" || got.Version != "1.2.3" {
		t.Errorf("unexpected identity after restart: %+v", got)
	}
	if !got.Capabilities.Docker {
		t.Error("capabilities not persisted across restart")
	}
	if got.SessionToken != registered.SessionToken {
		t.Error("session token not persisted across restart")
	}
	if !got.RegistrationTime.Equal(originalRegistration) {
		t.Errorf("registration time changed: want %v, got %v", originalRegistration, got.RegistrationTime)
	}
	if !got.LastHeartbeat.After(originalRegistration) {
		t.Errorf("persisted heartbeat not updated: want after %v, got %v", originalRegistration, got.LastHeartbeat)
	}
	if got.Status.RunningServices != 0 || got.Status.Uptime != 0 {
		t.Errorf("status is ephemeral and must reset on restart, got %+v", got.Status)
	}
}

func TestPersistentAgentStore_ListAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")

	store, _ := openTestRegistry(t, path)
	for _, id := range []string{"a", "b", "c"} {
		if _, err := store.RegisterAgent(id, "host-"+id, "1.0.0", AgentCapabilities{}); err != nil {
			t.Fatalf("RegisterAgent(%q): %v", id, err)
		}
	}

	restarted, _ := openTestRegistry(t, path)
	agents := restarted.ListAgents()

	if len(agents) != 3 {
		t.Fatalf("expected 3 agents after restart, got %d", len(agents))
	}

	seen := map[string]bool{}
	for _, a := range agents {
		seen[a.AgentID] = true
	}
	for _, id := range []string{"a", "b", "c"} {
		if !seen[id] {
			t.Errorf("agent %q missing after restart", id)
		}
	}
}

func TestPersistentAgentStore_ReregisterReplacesAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")

	store, _ := openTestRegistry(t, path)

	first, err := store.RegisterAgent("agent-1", "host-1", "1.0.0", AgentCapabilities{})
	if err != nil {
		t.Fatalf("first RegisterAgent: %v", err)
	}
	second, err := store.RegisterAgent("agent-1", "host-1", "2.0.0", AgentCapabilities{Docker: true})
	if err != nil {
		t.Fatalf("second RegisterAgent: %v", err)
	}
	if second.SessionToken == first.SessionToken {
		t.Error("re-registration must issue a new session token")
	}

	restarted, _ := openTestRegistry(t, path)
	got, err := restarted.GetAgent("agent-1")
	if err != nil {
		t.Fatalf("GetAgent after restart: %v", err)
	}
	if got.Version != "2.0.0" || !got.Capabilities.Docker {
		t.Errorf("expected re-registered record after restart, got %+v", got)
	}
	if got.SessionToken != second.SessionToken {
		t.Error("expected latest session token after restart")
	}
}

func TestPersistentAgentStore_DeletePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")

	store, _ := openTestRegistry(t, path)
	if _, err := store.RegisterAgent("agent-1", "host-1", "1.0.0", AgentCapabilities{}); err != nil {
		t.Fatalf("RegisterAgent: %v", err)
	}
	if err := store.DeleteAgent("agent-1"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}

	restarted, _ := openTestRegistry(t, path)
	if _, err := restarted.GetAgent("agent-1"); err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound after restart, got %v", err)
	}
	if agents := restarted.ListAgents(); len(agents) != 0 {
		t.Errorf("expected empty registry after restart, got %d agents", len(agents))
	}
}

func TestPersistentAgentStore_UnknownAgentHeartbeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")

	store, _ := openTestRegistry(t, path)
	if err := store.UpdateHeartbeat("missing", AgentStatus{}); err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestOpenSQLite_CreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "dir", "registry.db")

	if _, err := OpenSQLite(path); err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
}

func TestOpenSQLite_RestrictsDatabaseFilePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	path := filepath.Join(dir, "registry.db")

	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	db.Close()

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat database file: %v", err)
	}
	if perms := fileInfo.Mode().Perm(); perms&0o077 != 0 {
		t.Errorf("database file must be owner-only, got mode %#o", perms)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat database directory: %v", err)
	}
	if perms := dirInfo.Mode().Perm(); perms&0o077 != 0 {
		t.Errorf("database directory must be owner-only, got mode %#o", perms)
	}
}

func TestOpenSQLite_TightensExistingFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")

	// Simulate a database file created earlier with permissive mode.
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	db.Close()

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat database file: %v", err)
	}
	if perms := fileInfo.Mode().Perm(); perms&0o077 != 0 {
		t.Errorf("expected permissions to be tightened, got mode %#o", perms)
	}
}

func TestOpenSQLite_TightensExistingDirectoryPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	// Simulate a database directory created earlier with permissive mode.
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	db, err := OpenSQLite(filepath.Join(dir, "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	db.Close()

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat database directory: %v", err)
	}
	if perms := dirInfo.Mode().Perm(); perms&0o077 != 0 {
		t.Errorf("expected directory permissions to be tightened, got mode %#o", perms)
	}
}
