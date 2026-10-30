package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/byoi-platform/agent/internal/docker"
)

func TestManager_ExposesRestartBreakerStatus(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())
	m.SetRestartPolicy(RestartPolicy{MaxRestarts: 2, Window: time.Minute})

	spec := docker.ServiceSpec{
		Name:          "worker",
		Image:         "worker:1",
		RestartPolicy: RestartPolicyOnFailure,
	}
	if _, err := m.DeployService(context.Background(), spec); err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
			t.Fatalf("onUnhealthy failed: %v", err)
		}
	}

	svc, ok := m.GetService("worker")
	if !ok {
		t.Fatal("expected the service to still be managed")
	}
	if svc.State != ServiceStateFailed {
		t.Errorf("expected failed state, got %s", svc.State)
	}
	if svc.RestartAttempts != 2 {
		t.Errorf("expected 2 restart attempts, got %d", svc.RestartAttempts)
	}
	if svc.RestartBudgetRemaining != 0 {
		t.Errorf("expected no restart budget, got %d", svc.RestartBudgetRemaining)
	}
	if svc.RestartWindowSeconds != 60 {
		t.Errorf("expected a 60s restart window, got %d", svc.RestartWindowSeconds)
	}
	if svc.FailedReason != FailedReasonCircuitBreaker {
		t.Errorf("expected failed reason %q, got %q", FailedReasonCircuitBreaker, svc.FailedReason)
	}

	data, err := json.Marshal(svc)
	if err != nil {
		t.Fatalf("marshal service: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal service: %v", err)
	}
	for _, key := range []string{"restart_count", "restart_budget_remaining", "restart_window_seconds", "failed_reason"} {
		if _, ok := payload[key]; !ok {
			t.Errorf("expected %q in status JSON, got %s", key, data)
		}
	}
	if got := payload["restart_count"]; got != float64(2) {
		t.Errorf("expected restart_count 2, got %v", got)
	}
	if got := payload["RestartCount"]; got != float64(2) {
		t.Errorf("expected legacy RestartCount to remain 2, got %v", got)
	}
}

func TestService_StatusJSONUnlimitedRestartBudget(t *testing.T) {
	svc := Service{
		State:                  ServiceStateRunning,
		RestartCount:           7,
		RestartAttempts:        4,
		RestartBudgetRemaining: -1,
		RestartWindowSeconds:   600,
	}
	data, err := json.Marshal(svc)
	if err != nil {
		t.Fatalf("marshal service: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("unmarshal service: %v", err)
	}
	if got := payload["restart_count"]; got != float64(4) {
		t.Errorf("expected restart_count 4, got %v", got)
	}
	if got := payload["restart_budget_remaining"]; got != float64(-1) {
		t.Errorf("expected unlimited budget -1, got %v", got)
	}
	if got := payload["RestartCount"]; got != float64(7) {
		t.Errorf("expected legacy RestartCount 7, got %v", got)
	}
}

func TestManager_ExhaustedServiceKeepsZeroBudgetAfterWindowExpires(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())
	m.SetRestartPolicy(RestartPolicy{MaxRestarts: 1, Window: time.Minute})

	spec := docker.ServiceSpec{
		Name:          "worker",
		Image:         "worker:1",
		RestartPolicy: RestartPolicyOnFailure,
	}
	if _, err := m.DeployService(context.Background(), spec); err != nil {
		t.Fatalf("deploy failed: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
			t.Fatalf("onUnhealthy failed: %v", err)
		}
	}

	// Simulate the attempts aging out of the sliding window. The breaker is
	// still tripped, so the advertised budget must remain zero.
	m.mu.Lock()
	m.services["worker"].restart.attempts = []time.Time{time.Now().Add(-2 * time.Minute)}
	m.mu.Unlock()

	svc, _ := m.GetService("worker")
	if svc.RestartAttempts != 0 {
		t.Errorf("expected no attempts in the current window, got %d", svc.RestartAttempts)
	}
	if svc.RestartBudgetRemaining != 0 {
		t.Errorf("expected exhausted service to report zero budget, got %d", svc.RestartBudgetRemaining)
	}
	if svc.FailedReason != FailedReasonCircuitBreaker {
		t.Errorf("expected failed reason %q, got %q", FailedReasonCircuitBreaker, svc.FailedReason)
	}
}
