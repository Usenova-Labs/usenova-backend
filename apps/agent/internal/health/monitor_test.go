package health

import (
	"context"
	"testing"
	"time"
)

func TestMonitor_ServiceAddedAfterStartIsMonitored(t *testing.T) {
	called := make(chan string, 1)
	m := NewMonitor(testLogger(), func(ctx context.Context, serviceID string) error {
		select {
		case called <- serviceID:
		default:
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	defer m.Stop()

	// Add AFTER Start: the monitor must still pick it up.
	m.AddService("svc-after-start", CheckConfig{
		Type:             TCP,
		Host:             "127.0.0.1",
		Port:             1, // closed -> fails
		Timeout:          100 * time.Millisecond,
		Interval:         50 * time.Millisecond,
		FailureThreshold: 1,
	})

	select {
	case id := <-called:
		if id != "svc-after-start" {
			t.Errorf("expected svc-after-start, got %s", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("onUnhealthy was not called for service added after Start")
	}
}

func TestMonitor_HealthStateTransitions(t *testing.T) {
	m := NewMonitor(testLogger(), nil)

	m.AddService("svc", CheckConfig{
		Type:             TCP,
		Host:             "127.0.0.1",
		Port:             1,
		Timeout:          100 * time.Millisecond,
		Interval:         30 * time.Millisecond,
		FailureThreshold: 2,
	})

	if h, ok := m.GetServiceHealth("svc"); !ok || !h.IsHealthy {
		t.Fatal("expected newly added service to start healthy")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	defer m.Stop()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("service never transitioned to unhealthy")
		default:
			h, ok := m.GetServiceHealth("svc")
			if ok && !h.IsHealthy {
				if h.ConsecutiveFails < 2 {
					t.Errorf("expected >=2 consecutive fails, got %d", h.ConsecutiveFails)
				}
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestMonitor_RemoveServiceStopsMonitoring(t *testing.T) {
	m := NewMonitor(testLogger(), nil)
	m.AddService("svc", CheckConfig{
		Type:     TCP,
		Host:     "127.0.0.1",
		Port:     1,
		Timeout:  50 * time.Millisecond,
		Interval: 20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	defer m.Stop()

	m.RemoveService("svc")
	if _, ok := m.GetServiceHealth("svc"); ok {
		t.Error("expected service to be removed")
	}
}
