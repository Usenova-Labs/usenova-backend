package agent

import (
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

	created, err := store.Create(Deployment{
		AgentID:     "platform-1",
		ServiceName: "web",
		Image:       "nginx",
		CommandID:   "cmd-1",
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
