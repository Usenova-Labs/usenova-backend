package runtime

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/go-connections/nat"
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

	logCalls []logCall
	logsBody string
	logsErr  error
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

// logOpts records the options passed to GetContainerLogs, and logsErr/logsBody
// let tests simulate daemon errors and canned output.
type logCall struct {
	containerID string
	opts        docker.LogOptions
}

func (f *fakeRuntime) GetContainerLogs(ctx context.Context, containerID string, opts docker.LogOptions) (io.ReadCloser, error) {
	f.logCalls = append(f.logCalls, logCall{containerID: containerID, opts: opts})
	if f.logsErr != nil {
		return nil, f.logsErr
	}
	body := f.logsBody
	if body == "" {
		body = "line one\nline two\n"
	}
	return io.NopCloser(strings.NewReader(body)), nil
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

func TestManager_ServiceLogs(t *testing.T) {
	ctx := context.Background()

	t.Run("streams logs for a deployed service", func(t *testing.T) {
		fake := newFakeRuntime()
		fake.logsBody = "hello\nworld\n"
		m := NewManager(fake, testLogger())
		svc, err := m.DeployService(ctx, docker.ServiceSpec{Name: "web", Image: "nginx:1"})
		if err != nil {
			t.Fatalf("deploy failed: %v", err)
		}

		rc, err := m.ServiceLogs(ctx, "web", docker.LogOptions{Tail: "50", Follow: true})
		if err != nil {
			t.Fatalf("ServiceLogs failed: %v", err)
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		if string(b) != "hello\nworld\n" {
			t.Errorf("unexpected logs %q", b)
		}
		if len(fake.logCalls) != 1 || fake.logCalls[0].containerID != svc.ContainerID {
			t.Fatalf("unexpected log call: %+v", fake.logCalls)
		}
		if fake.logCalls[0].opts.Tail != "50" || !fake.logCalls[0].opts.Follow {
			t.Errorf("options not forwarded: %+v", fake.logCalls[0].opts)
		}
	})

	t.Run("unknown service returns ErrServiceNotFound", func(t *testing.T) {
		m := NewManager(newFakeRuntime(), testLogger())
		_, err := m.ServiceLogs(ctx, "ghost", docker.LogOptions{})
		if !errors.Is(err, ErrServiceNotFound) {
			t.Fatalf("expected ErrServiceNotFound, got %v", err)
		}
	})

	t.Run("docker unavailable returns ErrRuntimeUnavailable", func(t *testing.T) {
		fake := newFakeRuntime()
		m := NewManager(fake, testLogger())
		if _, err := m.DeployService(ctx, docker.ServiceSpec{Name: "web", Image: "nginx:1"}); err != nil {
			t.Fatalf("deploy failed: %v", err)
		}
		fake.available = false
		_, err := m.ServiceLogs(ctx, "web", docker.LogOptions{})
		if !errors.Is(err, ErrRuntimeUnavailable) {
			t.Fatalf("expected ErrRuntimeUnavailable, got %v", err)
		}
	})

	t.Run("service without container returns ErrServiceNotFound", func(t *testing.T) {
		fake := newFakeRuntime()
		m := NewManager(fake, testLogger())
		// Register a service with no ContainerID directly (as adoption might).
		m.mu.Lock()
		m.services["web"] = &Service{Spec: docker.ServiceSpec{Name: "web"}}
		m.mu.Unlock()

		_, err := m.ServiceLogs(ctx, "web", docker.LogOptions{})
		if !errors.Is(err, ErrServiceNotFound) {
			t.Fatalf("expected ErrServiceNotFound, got %v", err)
		}
	})
}

func TestBuildHealthCheck(t *testing.T) {
	t.Run("nil when no health config", func(t *testing.T) {
		if cfg := buildHealthCheck(docker.ServiceSpec{}, nil); cfg != nil {
			t.Errorf("expected nil, got %+v", cfg)
		}
	})

	t.Run("http maps container port to published host port", func(t *testing.T) {
		cfg := buildHealthCheck(docker.ServiceSpec{
			Ports: map[string]string{"8080": "80"},
			HealthCheck: &docker.HealthConfig{
				Type: "http",
				Port: 80,
				Path: "/ready",
			},
		}, nil)
		if cfg == nil {
			t.Fatal("expected config")
		}
		if cfg.Endpoint != "http://127.0.0.1:8080/ready" {
			t.Errorf("unexpected endpoint %s", cfg.Endpoint)
		}
	})

	t.Run("tcp maps container port to published host port", func(t *testing.T) {
		cfg := buildHealthCheck(docker.ServiceSpec{
			Ports:       map[string]string{"5432": "5432"},
			HealthCheck: &docker.HealthConfig{Type: "tcp", Port: 5432},
		}, nil)
		if cfg == nil {
			t.Fatal("expected config")
		}
		if cfg.Port != 5432 {
			t.Errorf("unexpected port %d", cfg.Port)
		}
	})

	t.Run("actual published port overrides spec", func(t *testing.T) {
		// Spec asked for host 8080 -> container 80, but Docker published
		// 49152 -> 80. The probe must target the real host port.
		cfg := buildHealthCheck(docker.ServiceSpec{
			Ports:       map[string]string{"8080": "80"},
			HealthCheck: &docker.HealthConfig{Type: "http", Port: 80, Path: "/health"},
		}, map[int]int{80: 49152})
		if cfg == nil {
			t.Fatal("expected config")
		}
		if cfg.Endpoint != "http://127.0.0.1:49152/health" {
			t.Errorf("expected published port 49152, got %s", cfg.Endpoint)
		}
	})

	t.Run("default port picks lowest container port deterministically", func(t *testing.T) {
		cfg := buildHealthCheck(docker.ServiceSpec{
			Ports:       map[string]string{"9000": "9000", "9001": "80", "9002": "443"},
			HealthCheck: &docker.HealthConfig{Type: "tcp"}, // no port specified
		}, nil)
		if cfg == nil {
			t.Fatal("expected config")
		}
		if cfg.Port != 9001 { // container port 80 is the lowest
			t.Errorf("expected host port 9001 for lowest container port, got %d", cfg.Port)
		}
	})

	t.Run("nil when no ports available", func(t *testing.T) {
		cfg := buildHealthCheck(docker.ServiceSpec{
			HealthCheck: &docker.HealthConfig{Type: "http"},
		}, nil)
		if cfg != nil {
			t.Errorf("expected nil, got %+v", cfg)
		}
	})
}

func TestPublishedPorts(t *testing.T) {
	info := &types.ContainerJSON{
		NetworkSettings: &types.NetworkSettings{
			NetworkSettingsBase: types.NetworkSettingsBase{
				Ports: nat.PortMap{
					"80/tcp":   []nat.PortBinding{{HostPort: "49152"}},
					"443/tcp":  []nat.PortBinding{{HostPort: "49153"}},
					"9999/tcp": nil,
				},
			},
		},
	}
	got := publishedPorts(info)
	if got[80] != 49152 || got[443] != 49153 {
		t.Errorf("unexpected published ports: %v", got)
	}
	if _, ok := got[9999]; ok {
		t.Error("port with no bindings should be skipped")
	}
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
