package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/docker"
)

func testLogger() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	return log
}

// fakeRuntime implements ContainerRuntime for unit tests.
type fakeRuntime struct {
	available bool

	pulled     []string
	runConfigs []docker.ContainerConfig
	restarted  []string
	stopped    []string
	removed    []string

	inspect map[string]types.ContainerJSON
	nextID  int
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{available: true, inspect: make(map[string]types.ContainerJSON)}
}

func (f *fakeRuntime) IsAvailable(ctx context.Context) bool { return f.available }

func (f *fakeRuntime) PullImage(ctx context.Context, image string, authConfig *types.AuthConfig) error {
	f.pulled = append(f.pulled, image)
	return nil
}

func (f *fakeRuntime) RunContainer(ctx context.Context, cfg docker.ContainerConfig) (string, error) {
	f.runConfigs = append(f.runConfigs, cfg)
	f.nextID++
	id := "container-" + string(rune('0'+f.nextID))
	f.inspect[id] = types.ContainerJSON{
		ContainerJSONBase: &types.ContainerJSONBase{
			ID:    id,
			Name:  cfg.Name,
			State: &types.ContainerState{Running: true, Status: "running"},
		},
	}
	return id, nil
}

func (f *fakeRuntime) StartContainer(ctx context.Context, containerID string) error { return nil }

func (f *fakeRuntime) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	f.stopped = append(f.stopped, containerID)
	return nil
}

func (f *fakeRuntime) RestartContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	f.restarted = append(f.restarted, containerID)
	return nil
}

func (f *fakeRuntime) RemoveContainer(ctx context.Context, containerID string, force bool) error {
	f.removed = append(f.removed, containerID)
	return nil
}

func (f *fakeRuntime) InspectContainer(ctx context.Context, containerID string) (*types.ContainerJSON, error) {
	info, ok := f.inspect[containerID]
	if !ok {
		return nil, errNotFound
	}
	return &info, nil
}

func (f *fakeRuntime) ListContainers(ctx context.Context, all bool, filter filters.Args) ([]types.Container, error) {
	return nil, nil
}

var errNotFound = &notFoundError{}

type notFoundError struct{}

func (e *notFoundError) Error() string { return "container not found" }

func TestManager_DeployServiceAndRunningCount(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())

	if got := m.RunningCount(); got != 0 {
		t.Fatalf("expected 0 running services initially, got %d", got)
	}

	spec := docker.ServiceSpec{
		Name:  "web",
		Image: "nginx:latest",
		Ports: map[string]string{"8080": "80"},
		Env:   []string{"FOO=bar"},
		HealthCheck: &docker.HealthConfig{
			Type:     "http",
			Path:     "/health",
			Interval: time.Second,
			Timeout:  time.Second,
		},
	}

	svc, err := m.DeployService(context.Background(), spec)
	if err != nil {
		t.Fatalf("DeployService failed: %v", err)
	}
	if svc.State != ServiceStateRunning {
		t.Errorf("expected running state, got %s", svc.State)
	}
	if len(fake.pulled) != 1 || fake.pulled[0] != "nginx:latest" {
		t.Errorf("expected image pull, got %v", fake.pulled)
	}
	if len(fake.runConfigs) != 1 {
		t.Fatalf("expected 1 container run, got %d", len(fake.runConfigs))
	}
	if fake.runConfigs[0].Labels[labelService] != "web" {
		t.Errorf("expected service label, got %v", fake.runConfigs[0].Labels)
	}

	if got := m.RunningCount(); got != 1 {
		t.Errorf("expected 1 running service, got %d", got)
	}

	if _, err := m.DeployService(context.Background(), spec); err == nil {
		t.Error("expected error when deploying an already running service")
	}
}

func TestManager_OnUnhealthyRestartsAccordingToPolicy(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())

	spec := docker.ServiceSpec{
		Name:          "worker",
		Image:         "worker:1",
		RestartPolicy: RestartPolicyOnFailure,
	}
	if _, err := m.DeployService(context.Background(), spec); err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}
	if len(fake.restarted) != 1 {
		t.Errorf("expected 1 restart, got %d", len(fake.restarted))
	}
	svc, _ := m.GetService("worker")
	if svc.RestartCount != 1 {
		t.Errorf("expected restart count 1, got %d", svc.RestartCount)
	}

	// Policy "no" must not restart.
	noRestartSpec := docker.ServiceSpec{
		Name:          "static",
		Image:         "static:1",
		RestartPolicy: RestartPolicyNo,
	}
	if _, err := m.DeployService(context.Background(), noRestartSpec); err != nil {
		t.Fatalf("deploy failed: %v", err)
	}
	if err := m.onUnhealthy(context.Background(), "static"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}
	if len(fake.restarted) != 1 {
		t.Errorf("expected no additional restart, got %d", len(fake.restarted))
	}
	svc, _ = m.GetService("static")
	if svc.State != ServiceStateUnhealthy {
		t.Errorf("expected unhealthy state, got %s", svc.State)
	}
}

func TestManager_StopAndRemoveService(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())

	if _, err := m.DeployService(context.Background(), docker.ServiceSpec{Name: "app", Image: "app:1"}); err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	if err := m.StopService(context.Background(), "app"); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if got := m.RunningCount(); got != 0 {
		t.Errorf("expected 0 running after stop, got %d", got)
	}

	if err := m.RemoveService(context.Background(), "app"); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if _, ok := m.GetService("app"); ok {
		t.Error("expected service to be gone after remove")
	}
}

func TestBuildHealthCheck(t *testing.T) {
	t.Run("nil when no health config", func(t *testing.T) {
		if cfg := buildHealthCheck(docker.ServiceSpec{}); cfg != nil {
			t.Errorf("expected nil, got %+v", cfg)
		}
	})

	t.Run("http maps to published host port", func(t *testing.T) {
		cfg := buildHealthCheck(docker.ServiceSpec{
			Ports: map[string]string{"8080": "80"},
			HealthCheck: &docker.HealthConfig{
				Type: "http",
				Port: 80,
				Path: "/ready",
			},
		})
		if cfg == nil {
			t.Fatal("expected config")
		}
		if cfg.Endpoint != "http://127.0.0.1:8080/ready" {
			t.Errorf("unexpected endpoint %s", cfg.Endpoint)
		}
	})

	t.Run("tcp maps to published host port", func(t *testing.T) {
		cfg := buildHealthCheck(docker.ServiceSpec{
			Ports:       map[string]string{"5432": "5432"},
			HealthCheck: &docker.HealthConfig{Type: "tcp", Port: 5432},
		})
		if cfg == nil {
			t.Fatal("expected config")
		}
		if cfg.Port != 5432 {
			t.Errorf("unexpected port %d", cfg.Port)
		}
	})

	t.Run("nil when no ports available", func(t *testing.T) {
		cfg := buildHealthCheck(docker.ServiceSpec{
			HealthCheck: &docker.HealthConfig{Type: "http"},
		})
		if cfg != nil {
			t.Errorf("expected nil, got %+v", cfg)
		}
	})
}

func TestShouldRestart(t *testing.T) {
	cases := map[string]bool{
		RestartPolicyAlways:        true,
		RestartPolicyUnlessStopped: true,
		RestartPolicyOnFailure:     true,
		RestartPolicyNo:            false,
		"":                         false,
	}
	for policy, want := range cases {
		if got := shouldRestart(policy); got != want {
			t.Errorf("shouldRestart(%q) = %v, want %v", policy, got, want)
		}
	}
}
