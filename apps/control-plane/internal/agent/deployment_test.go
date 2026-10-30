package agent

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestDeploymentStore_CreateGetListAndUpdate(t *testing.T) {
	store := NewDeploymentStore()

	first, err := store.Create(Deployment{
		AgentID:     "platform-1",
		ServiceName: "web",
		Image:       "nginx:alpine",
		CommandID:   "cmd-1",
		Requester:   "operator",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if first.ID == "" || first.State != DeploymentStatePending {
		t.Fatalf("unexpected deployment: %+v", first)
	}

	second, err := store.Create(Deployment{
		AgentID:     "platform-2",
		ServiceName: "api",
		Image:       "api:latest",
		State:       DeploymentStateFailed,
	})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}

	got, err := store.Get(first.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ServiceName != "web" || got.AgentID != "platform-1" {
		t.Errorf("Get returned %+v", got)
	}

	list := store.List(DeploymentFilter{AgentID: "platform-1"})
	if len(list) != 1 || list[0].ID != first.ID {
		t.Errorf("List(agent) = %+v", list)
	}
	list = store.List(DeploymentFilter{State: DeploymentStateFailed})
	if len(list) != 1 || list[0].ID != second.ID {
		t.Errorf("List(state) = %+v", list)
	}

	updated, err := store.UpdateStateByCommandID("cmd-1", DeploymentStateRunning)
	if err != nil {
		t.Fatalf("UpdateStateByCommandID: %v", err)
	}
	if updated.State != DeploymentStateRunning {
		t.Errorf("state = %q, want running", updated.State)
	}

	updated, err = store.UpdateStateByCommandID("cmd-1", DeploymentStateSucceeded)
	if err != nil {
		t.Fatalf("UpdateStateByCommandID succeeded: %v", err)
	}
	if updated.State != DeploymentStateSucceeded {
		t.Errorf("state = %q, want succeeded", updated.State)
	}

	// Terminal states are final.
	updated, err = store.UpdateStateByCommandID("cmd-1", DeploymentStateRunning)
	if err != nil {
		t.Fatalf("UpdateStateByCommandID terminal: %v", err)
	}
	if updated.State != DeploymentStateSucceeded {
		t.Errorf("state = %q, want succeeded to remain final", updated.State)
	}

	if _, err := store.UpdateStateByCommandID("missing", DeploymentStateRunning); err != ErrDeploymentNotFound {
		t.Errorf("missing command error = %v, want ErrDeploymentNotFound", err)
	}
}

func TestDeploymentStore_RejectsDuplicateCommand(t *testing.T) {
	store := NewDeploymentStore()

	if _, err := store.Create(Deployment{
		AgentID: "platform-1", ServiceName: "web", Image: "nginx", CommandID: "cmd-1",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.Create(Deployment{
		AgentID: "platform-2", ServiceName: "api", Image: "api", CommandID: "cmd-1",
	}); err == nil {
		t.Fatal("expected duplicate command ID to be rejected")
	}
}

func TestDeploymentStore_PersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane.db")

	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	store, err := NewPersistentDeploymentStore(db)
	if err != nil {
		t.Fatalf("NewPersistentDeploymentStore: %v", err)
	}

	escrowID := uint64(42)
	created, err := store.Create(Deployment{
		AgentID:     "platform-1",
		ServiceName: "web",
		Image:       "nginx",
		CommandID:   "cmd-1",
		EscrowID:    &escrowID,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.UpdateStateByCommandID("cmd-1", DeploymentStateSucceeded); err != nil {
		t.Fatalf("UpdateStateByCommandID: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	db, err = OpenSQLite(path)
	if err != nil {
		t.Fatalf("reopen SQLite: %v", err)
	}
	defer db.Close()

	reloaded, err := NewPersistentDeploymentStore(db)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	got, err := reloaded.Get(created.ID)
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.State != DeploymentStateSucceeded || got.CommandID != "cmd-1" || got.ServiceName != "web" {
		t.Errorf("reloaded deployment = %+v", got)
	}
	if got.EscrowID == nil || *got.EscrowID != escrowID {
		t.Errorf("reloaded escrow_id = %v, want %d", got.EscrowID, escrowID)
	}

	updated, err := reloaded.UpdateStateByCommandID("cmd-1", DeploymentStateRunning)
	if err != nil {
		t.Fatalf("UpdateStateByCommandID after reload: %v", err)
	}
	if updated.State != DeploymentStateSucceeded {
		t.Errorf("terminal state was not preserved: %q", updated.State)
	}
}

func TestDeploymentStore_Timestamps(t *testing.T) {
	store := NewDeploymentStore()
	d, err := store.Create(Deployment{
		AgentID: "platform-1", ServiceName: "web", Image: "nginx",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if d.CreatedAt.IsZero() || d.UpdatedAt.IsZero() {
		t.Fatalf("timestamps not set: %+v", d)
	}
	if time.Since(d.CreatedAt) > time.Minute {
		t.Errorf("created_at looks wrong: %v", d.CreatedAt)
	}
}

func TestDeploymentStore_AttachEscrowID(t *testing.T) {
	store := NewDeploymentStore()

	d, err := store.Create(Deployment{AgentID: "platform-1", ServiceName: "web", Image: "nginx"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if d.EscrowID != nil {
		t.Fatalf("new deployment already has escrow: %d", *d.EscrowID)
	}

	escrowID := uint64(99)
	linked, err := store.AttachEscrowID(d.ID, escrowID)
	if err != nil {
		t.Fatalf("AttachEscrowID: %v", err)
	}
	if linked.EscrowID == nil || *linked.EscrowID != escrowID {
		t.Fatalf("linked escrow_id = %v, want %d", linked.EscrowID, escrowID)
	}

	// Same id is idempotent.
	if _, err := store.AttachEscrowID(d.ID, escrowID); err != nil {
		t.Fatalf("re-attach same id: %v", err)
	}

	// A different id is refused.
	if _, err := store.AttachEscrowID(d.ID, escrowID+1); !errors.Is(err, ErrEscrowAlreadyLinked) {
		t.Fatalf("attach different id error = %v, want ErrEscrowAlreadyLinked", err)
	}

	if _, err := store.AttachEscrowID("missing", escrowID); !errors.Is(err, ErrDeploymentNotFound) {
		t.Fatalf("attach missing error = %v, want ErrDeploymentNotFound", err)
	}
}

func TestMigrateDeploymentEscrowOnLegacyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane.db")

	// Simulate a database created before escrow linkage existed.
	db, err := sql.Open(sqliteDriver, path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE deployments (
		id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, service_name TEXT NOT NULL,
		image TEXT NOT NULL, state TEXT NOT NULL, command_id TEXT NOT NULL DEFAULT '',
		requester TEXT NOT NULL DEFAULT '', metadata TEXT NOT NULL DEFAULT '{}',
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create legacy deployments table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	db, err = OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer db.Close()

	columns, err := tableColumns(db, "deployments")
	if err != nil {
		t.Fatalf("tableColumns: %v", err)
	}
	if !columns["escrow_id"] {
		t.Fatal("escrow_id column was not added by migration")
	}

	store, err := NewPersistentDeploymentStore(db)
	if err != nil {
		t.Fatalf("NewPersistentDeploymentStore: %v", err)
	}
	escrowID := uint64(5)
	d, err := store.Create(Deployment{
		AgentID: "platform-1", ServiceName: "web", Image: "nginx", EscrowID: &escrowID,
	})
	if err != nil {
		t.Fatalf("Create after migration: %v", err)
	}
	if d.EscrowID == nil || *d.EscrowID != escrowID {
		t.Errorf("escrow_id = %v, want %d", d.EscrowID, escrowID)
	}
}
