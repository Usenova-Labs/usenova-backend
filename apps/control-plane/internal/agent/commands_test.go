package agent

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCommandQueue_EnqueueAndNext(t *testing.T) {
	q := NewCommandQueue(0, 0)

	record, err := q.Enqueue("agent-1", Command{
		Type:    CommandTypeDeploy,
		Payload: json.RawMessage(`{"name":"api","image":"nginx"}`),
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if record.Command.ID == "" {
		t.Error("expected a command ID to be assigned")
	}
	if record.Status != CommandStatusPending {
		t.Errorf("expected status %q, got %q", CommandStatusPending, record.Status)
	}

	next, err := q.Next("agent-1")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if next.Command.ID != record.Command.ID {
		t.Errorf("expected command %q, got %q", record.Command.ID, next.Command.ID)
	}
	if next.Status != CommandStatusRunning {
		t.Errorf("expected status %q, got %q", CommandStatusRunning, next.Status)
	}

	// A second poll must not hand out the same command again while the lease
	// is valid.
	if _, err := q.Next("agent-1"); err != ErrNoPendingCommand {
		t.Errorf("expected ErrNoPendingCommand, got %v", err)
	}
}

func TestCommandQueue_NextEmptyQueue(t *testing.T) {
	q := NewCommandQueue(0, 0)

	if _, err := q.Next("agent-1"); err != ErrNoPendingCommand {
		t.Errorf("expected ErrNoPendingCommand, got %v", err)
	}
}

func TestCommandQueue_Complete(t *testing.T) {
	q := NewCommandQueue(0, 0)

	record, err := q.Enqueue("agent-1", Command{Type: CommandTypeStop, Payload: json.RawMessage(`{"name":"api"}`)})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := q.Next("agent-1"); err != nil {
		t.Fatalf("next: %v", err)
	}

	completed, err := q.Complete("agent-1", record.Command.ID, CommandResult{
		CommandID: record.Command.ID,
		Status:    CommandStatusSucceeded,
		Message:   "service api stopped",
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completed.Status != CommandStatusSucceeded {
		t.Errorf("expected status %q, got %q", CommandStatusSucceeded, completed.Status)
	}
	if completed.Result == nil || completed.Result.Message != "service api stopped" {
		t.Errorf("expected the result to be recorded, got %+v", completed.Result)
	}

	// A completed command is not handed out again.
	if _, err := q.Next("agent-1"); err != ErrNoPendingCommand {
		t.Errorf("expected ErrNoPendingCommand, got %v", err)
	}
}

func TestCommandQueue_CompleteIsIdempotent(t *testing.T) {
	q := NewCommandQueue(0, 0)

	record, err := q.Enqueue("agent-1", Command{Type: CommandTypeStop, Payload: json.RawMessage(`{"name":"api"}`)})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := q.Next("agent-1"); err != nil {
		t.Fatalf("next: %v", err)
	}

	if _, err := q.Complete("agent-1", record.Command.ID, CommandResult{
		CommandID: record.Command.ID,
		Status:    CommandStatusSucceeded,
		Message:   "first",
	}); err != nil {
		t.Fatalf("complete: %v", err)
	}

	second, err := q.Complete("agent-1", record.Command.ID, CommandResult{
		CommandID: record.Command.ID,
		Status:    CommandStatusFailed,
		Message:   "second",
	})
	if err != nil {
		t.Fatalf("second complete should not fail: %v", err)
	}
	if second.Status != CommandStatusSucceeded {
		t.Errorf("expected the first result to win, got status %q", second.Status)
	}
}

func TestCommandQueue_CompleteUnknownCommand(t *testing.T) {
	q := NewCommandQueue(0, 0)

	if _, err := q.Complete("agent-1", "nope", CommandResult{Status: CommandStatusSucceeded}); err != ErrCommandNotFound {
		t.Errorf("expected ErrCommandNotFound, got %v", err)
	}
}

func TestCommandQueue_QueueFull(t *testing.T) {
	q := NewCommandQueue(2, 0)

	for i := 0; i < 2; i++ {
		if _, err := q.Enqueue("agent-1", Command{Type: CommandTypeStop, Payload: json.RawMessage(`{"name":"api"}`)}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	if _, err := q.Enqueue("agent-1", Command{Type: CommandTypeStop, Payload: json.RawMessage(`{"name":"api"}`)}); err != ErrCommandQueueFull {
		t.Errorf("expected ErrCommandQueueFull, got %v", err)
	}

	// Other agents are not affected by a full queue.
	if _, err := q.Enqueue("agent-2", Command{Type: CommandTypeStop, Payload: json.RawMessage(`{"name":"api"}`)}); err != nil {
		t.Errorf("expected the queue of another agent to be independent: %v", err)
	}
}

func TestCommandQueue_ExpiredCommandIsNotExecuted(t *testing.T) {
	q := NewCommandQueue(0, 0)

	expired := time.Now().Add(-time.Minute)
	record, err := q.Enqueue("agent-1", Command{
		Type:      CommandTypeStop,
		Payload:   json.RawMessage(`{"name":"api"}`),
		ExpiresAt: &expired,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if _, err := q.Next("agent-1"); err != ErrNoPendingCommand {
		t.Errorf("expected ErrNoPendingCommand, got %v", err)
	}

	got, err := q.Get("agent-1", record.Command.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != CommandStatusFailed {
		t.Errorf("expected the expired command to be failed, got %q", got.Status)
	}
	if got.Result == nil || got.Result.Error == "" {
		t.Error("expected the expired command to carry an error")
	}
}

func TestCommandQueue_LeaseExpiryRedelivers(t *testing.T) {
	q := NewCommandQueue(0, time.Millisecond)

	record, err := q.Enqueue("agent-1", Command{Type: CommandTypeStop, Payload: json.RawMessage(`{"name":"api"}`)})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	first, err := q.Next("agent-1")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if first.StartedAt == nil {
		t.Fatal("expected the command to have a start time")
	}
	firstStart := *first.StartedAt

	time.Sleep(5 * time.Millisecond)

	second, err := q.Next("agent-1")
	if err != nil {
		t.Fatalf("expected the command to be handed out again after the lease expired: %v", err)
	}
	if second.Command.ID != record.Command.ID {
		t.Errorf("expected command %q, got %q", record.Command.ID, second.Command.ID)
	}
	if second.StartedAt == nil || !second.StartedAt.After(firstStart) {
		t.Error("expected the re-leased command to have a newer start time")
	}
}

func TestCommandQueue_ListAndGet(t *testing.T) {
	q := NewCommandQueue(0, 0)

	record, err := q.Enqueue("agent-1", Command{Type: CommandTypeDeploy, Payload: json.RawMessage(`{"name":"api","image":"nginx"}`)})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	records := q.List("agent-1")
	if len(records) != 1 {
		t.Fatalf("expected 1 command, got %d", len(records))
	}

	got, err := q.Get("agent-1", record.Command.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Command.Type != CommandTypeDeploy {
		t.Errorf("expected type %q, got %q", CommandTypeDeploy, got.Command.Type)
	}

	if _, err := q.Get("agent-1", "missing"); err != ErrCommandNotFound {
		t.Errorf("expected ErrCommandNotFound, got %v", err)
	}
	if len(q.List("agent-2")) != 0 {
		t.Error("expected no commands for an unknown agent")
	}
}

func TestValidateCommandType(t *testing.T) {
	if err := validateCommandType(CommandTypeDeploy); err != nil {
		t.Errorf("deploy should be supported: %v", err)
	}
	if err := validateCommandType("restart"); err == nil {
		t.Error("expected an error for an unsupported command type")
	}
}

func TestValidateCommandPayload(t *testing.T) {
	cases := []struct {
		name    string
		cmdType CommandType
		payload string
		wantErr bool
	}{
		{"deploy with name and image", CommandTypeDeploy, `{"name":"api","image":"nginx"}`, false},
		{"deploy without image", CommandTypeDeploy, `{"name":"api"}`, true},
		{"deploy with empty image", CommandTypeDeploy, `{"name":"api","image":""}`, true},
		{"deploy without payload", CommandTypeDeploy, ``, true},
		{"deploy with non object payload", CommandTypeDeploy, `"nginx"`, true},
		{"stop with name", CommandTypeStop, `{"name":"api"}`, false},
		{"stop without name", CommandTypeStop, `{}`, true},
		{"remove with name", CommandTypeRemove, `{"name":"api"}`, false},
		{"remove with empty name", CommandTypeRemove, `{"name":""}`, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateCommandPayload(c.cmdType, json.RawMessage(c.payload))
			if c.wantErr && err == nil {
				t.Error("expected an error")
			}
			if !c.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
