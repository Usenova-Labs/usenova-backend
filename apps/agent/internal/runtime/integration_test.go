//go:build integration

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/docker"
)

func TestIntegration_DeployStopRemove(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	dc, err := docker.NewClient(log)
	if err != nil {
		t.Skipf("docker client not available: %v", err)
	}
	defer dc.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if !dc.IsAvailable(ctx) {
		t.Skip("docker daemon not available")
	}

	m := NewManager(dc, log)
	if err := m.Start(ctx); err != nil {
		t.Skipf("runtime manager start failed: %v", err)
	}
	defer m.Stop(context.Background())

	spec := docker.ServiceSpec{
		Name:  "test-integration",
		Image: "busybox:latest",
		Env:   []string{"TEST=1"},
		Ports: map[string]string{"0": "80"},
		HealthCheck: &docker.HealthConfig{
			Type:     "tcp",
			Interval: 500 * time.Millisecond,
			Timeout:  200 * time.Millisecond,
		},
		RestartPolicy: RestartPolicyOnFailure,
	}

	svc, err := m.DeployService(ctx, spec)
	if err != nil {
		t.Fatalf("deploy failed: %v", err)
	}
	if svc.State != ServiceStateRunning {
		t.Errorf("expected running, got %s", svc.State)
	}

	if err := m.StopService(ctx, "test-integration"); err != nil {
		t.Fatalf("stop failed: %v", err)
	}

	if err := m.RemoveService(ctx, "test-integration"); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if _, ok := m.GetService("test-integration"); ok {
		t.Error("service should not exist after removal")
	}
}
