package runtime

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/docker"
)

func testLogger() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	return log
}

// fakeRuntime implements ContainerRuntime for unit tests. It is safe for
// concurrent use: deferred restarts run on their own goroutine.
type fakeRuntime struct {
	mu        sync.Mutex
	available bool

	pulled     []string
	runConfigs []docker.ContainerConfig
	restarted  []string
	started    []string
	stopped    []string
	removed    []string

	inspect map[string]container.InspectResponse
	nextID  int

	logCalls []logCall
	logsBody string
	logsErr  error

	// stopErr is returned by StopContainer when set. It lets tests simulate a
	// stop that fails and has to be retried by the reconcile loop.
	stopErr error
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{available: true, inspect: make(map[string]container.InspectResponse)}
}

func (f *fakeRuntime) IsAvailable(ctx context.Context) bool { return f.available }

func (f *fakeRuntime) PullImage(ctx context.Context, image string, authConfig *registry.AuthConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pulled = append(f.pulled, image)
	return nil
}

func (f *fakeRuntime) RunContainer(ctx context.Context, cfg docker.ContainerConfig) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runConfigs = append(f.runConfigs, cfg)
	f.nextID++
	id := "container-" + string(rune('0'+f.nextID))
	f.inspect[id] = container.InspectResponse{
		ID:    id,
		Name:  cfg.Name,
		State: &container.State{Running: true, Status: "running"},
	}
	return id, nil
}

func (f *fakeRuntime) StartContainer(ctx context.Context, containerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, containerID)
	return nil
}

func (f *fakeRuntime) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, containerID)
	return f.stopErr
}

func (f *fakeRuntime) RestartContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarted = append(f.restarted, containerID)
	return nil
}

func (f *fakeRuntime) RemoveContainer(ctx context.Context, containerID string, force bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, containerID)
	return nil
}

func (f *fakeRuntime) InspectContainer(ctx context.Context, containerID string) (*container.InspectResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.inspect[containerID]
	if !ok {
		return nil, errNotFound
	}
	return &info, nil
}

func (f *fakeRuntime) ListContainers(ctx context.Context, all bool, filter client.Filters) ([]container.Summary, error) {
	return nil, nil
}

// restartCount returns how many containers were restarted. It locks because
// deferred restarts run on their own goroutine.
func (f *fakeRuntime) restartCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.restarted)
}

// startCount returns how many containers were started.
func (f *fakeRuntime) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

// stopCount returns how many stop operations were attempted.
func (f *fakeRuntime) stopCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stopped)
}

// setRunning flips the recorded state of a container so tests can simulate a
// container that exited.
func (f *fakeRuntime) setRunning(containerID string, running bool, exitCode int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.inspect[containerID]
	if !ok {
		return
	}
	if info.State == nil {
		info.State = &container.State{}
	}
	info.State.Running = running
	info.State.ExitCode = exitCode
	f.inspect[containerID] = info
}

// logOpts records the options passed to GetContainerLogs, and logsErr/logsBody
// let tests simulate daemon errors and canned output.
type logCall struct {
	containerID string
	opts        docker.LogOptions
}

func (f *fakeRuntime) GetContainerLogs(ctx context.Context, containerID string, opts docker.LogOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	info := &container.InspectResponse{
		NetworkSettings: &container.NetworkSettings{
			Ports: network.PortMap{
				network.MustParsePort("80/tcp"):   {{HostPort: "49152"}},
				network.MustParsePort("443/tcp"):  {{HostPort: "49153"}},
				network.MustParsePort("9999/tcp"): nil,
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

func TestManager_RestartLimitFailsService(t *testing.T) {
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

	for i := 0; i < 2; i++ {
		if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
			t.Fatalf("onUnhealthy failed: %v", err)
		}
	}
	if got := fake.restartCount(); got != 2 {
		t.Fatalf("expected 2 restarts, got %d", got)
	}

	// The third attempt exceeds the limit: no restart, the service fails.
	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}
	if got := fake.restartCount(); got != 2 {
		t.Errorf("expected no restart past the limit, got %d", got)
	}

	svc, ok := m.GetService("worker")
	if !ok {
		t.Fatal("expected the service to still be managed")
	}
	if svc.State != ServiceStateFailed {
		t.Errorf("expected failed state, got %s", svc.State)
	}
	if svc.LastError == nil {
		t.Error("expected the failure to be explained in LastError")
	}
	if svc.RestartAttempts != 2 {
		t.Errorf("expected 2 recorded attempts, got %d", svc.RestartAttempts)
	}
	if svc.LastRestartAt.IsZero() {
		t.Error("expected the last restart time to be recorded")
	}
	if svc.RestartCount != 2 {
		t.Errorf("expected 2 successful restarts, got %d", svc.RestartCount)
	}
	if got := m.FailedCount(); got != 1 {
		t.Errorf("expected 1 failed service, got %d", got)
	}

	// A service that ran out of budget stays down: further unhealthy signals
	// must not restart it again.
	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}
	if got := fake.restartCount(); got != 2 {
		t.Errorf("expected no further restarts, got %d", got)
	}
}

func TestManager_RestartLimitAppliesPerService(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())
	m.SetRestartPolicy(RestartPolicy{MaxRestarts: 5, Window: time.Minute})

	// The service overrides the agent-wide limit.
	spec := docker.ServiceSpec{
		Name:          "worker",
		Image:         "worker:1",
		RestartPolicy: RestartPolicyOnFailure,
		MaxRestarts:   1,
	}
	if _, err := m.DeployService(context.Background(), spec); err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}
	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}
	if got := fake.restartCount(); got != 1 {
		t.Errorf("expected 1 restart, got %d", got)
	}

	svc, _ := m.GetService("worker")
	if svc.State != ServiceStateFailed {
		t.Errorf("expected failed state, got %s", svc.State)
	}
}

func TestManager_RestartBackoffDefersRestart(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())
	m.SetRestartPolicy(RestartPolicy{MaxRestarts: 5, Window: time.Minute, Backoff: 100 * time.Millisecond})

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
	if got := fake.restartCount(); got != 1 {
		t.Fatalf("expected 1 restart, got %d", got)
	}

	// The next attempt is inside the backoff window: it must not run yet.
	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}
	if got := fake.restartCount(); got != 1 {
		t.Fatalf("expected the restart to be deferred, got %d restarts", got)
	}

	// Once the backoff elapsed the deferred restart runs.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fake.restartCount() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := fake.restartCount(); got != 2 {
		t.Errorf("expected the deferred restart to run, got %d restarts", got)
	}
}

func TestManager_ReconcileRestartLimitFailsService(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())
	m.SetRestartPolicy(RestartPolicy{MaxRestarts: 2, Window: time.Minute})

	spec := docker.ServiceSpec{
		Name:          "worker",
		Image:         "worker:1",
		RestartPolicy: RestartPolicyOnFailure,
	}
	svc, err := m.DeployService(context.Background(), spec)
	if err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	// The container keeps crashing: every reconcile cycle tries to start it
	// again until the restart budget is exhausted.
	for i := 0; i < 2; i++ {
		fake.setRunning(svc.ContainerID, false, 1)
		m.reconcile(context.Background())
	}
	if got := fake.startCount(); got != 2 {
		t.Fatalf("expected 2 container starts, got %d", got)
	}

	fake.setRunning(svc.ContainerID, false, 1)
	m.reconcile(context.Background())
	if got := fake.startCount(); got != 2 {
		t.Errorf("expected no start past the restart limit, got %d", got)
	}

	svc, _ = m.GetService("worker")
	if svc.State != ServiceStateFailed {
		t.Errorf("expected failed state, got %s", svc.State)
	}
	if got := m.FailedCount(); got != 1 {
		t.Errorf("expected 1 failed service, got %d", got)
	}
}

func TestManager_DeployResetsRestartBudget(t *testing.T) {
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

	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}
	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}
	if got := fake.restartCount(); got != 1 {
		t.Fatalf("expected 1 restart, got %d", got)
	}

	svc, _ := m.GetService("worker")
	if svc.State != ServiceStateFailed {
		t.Fatalf("expected failed state, got %s", svc.State)
	}

	// Redeploying the service gives it a fresh restart budget.
	if _, err := m.DeployService(context.Background(), spec); err != nil {
		t.Fatalf("redeploy failed: %v", err)
	}
	svc, _ = m.GetService("worker")
	if svc.RestartAttempts != 0 {
		t.Errorf("expected a fresh restart budget, got %d attempts", svc.RestartAttempts)
	}
	if svc.State != ServiceStateRunning {
		t.Errorf("expected running state, got %s", svc.State)
	}
}

func TestManager_ExhaustedServiceStaysFailed(t *testing.T) {
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

	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}
	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}

	svc, _ := m.GetService("worker")
	if svc.State != ServiceStateFailed {
		t.Fatalf("expected failed state, got %s", svc.State)
	}

	// The reconcile loop must not resurrect a service that used up its
	// restart budget.
	m.reconcile(context.Background())

	svc, _ = m.GetService("worker")
	if svc.State != ServiceStateFailed {
		t.Errorf("expected the service to stay failed, got %s", svc.State)
	}
	if got := m.FailedCount(); got != 1 {
		t.Errorf("expected 1 failed service, got %d", got)
	}
}

func TestManager_SetRestartPolicyKeepsBaseBackoff(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())

	// A base backoff above the cap must not be clamped down to the cap.
	m.SetRestartPolicy(RestartPolicy{
		MaxRestarts: 3,
		Window:      time.Minute,
		Backoff:     10 * time.Minute,
		MaxBackoff:  5 * time.Minute,
	})

	policy := m.policyForSpec(docker.ServiceSpec{})
	if policy.MaxBackoff < policy.Backoff {
		t.Errorf("expected the cap to be raised to the base backoff, got cap %s < base %s",
			policy.MaxBackoff, policy.Backoff)
	}
}

func TestManager_DeferredRestartIsNotDuplicated(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())
	m.SetRestartPolicy(RestartPolicy{MaxRestarts: 10, Window: time.Minute, Backoff: 300 * time.Millisecond})

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
	if got := fake.restartCount(); got != 1 {
		t.Fatalf("expected 1 restart, got %d", got)
	}

	// Several unhealthy transitions while the backoff is running must not
	// queue several deferred restarts.
	for i := 0; i < 4; i++ {
		if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
			t.Fatalf("onUnhealthy failed: %v", err)
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fake.restartCount() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Give any duplicated deferred restart time to fire.
	time.Sleep(600 * time.Millisecond)

	if got := fake.restartCount(); got != 2 {
		t.Errorf("expected exactly 2 restarts (one immediate, one deferred), got %d", got)
	}

	svc, _ := m.GetService("worker")
	if got := svc.RestartAttempts; got != 2 {
		t.Errorf("expected 2 restart attempts, got %d", got)
	}
}

func TestManager_DeferredRestartSkippedWhenServiceRecovered(t *testing.T) {
	fake := newFakeRuntime()
	m := NewManager(fake, testLogger())
	m.SetRestartPolicy(RestartPolicy{MaxRestarts: 10, Window: time.Minute, Backoff: 200 * time.Millisecond})

	spec := docker.ServiceSpec{
		Name:          "worker",
		Image:         "worker:1",
		RestartPolicy: RestartPolicyOnFailure,
		Ports:         map[string]string{"9090": "8080"},
		HealthCheck: &docker.HealthConfig{
			Type:     "http",
			Path:     "/health",
			Port:     8080,
			Interval: time.Hour,
			Retries:  1,
		},
	}
	if _, err := m.DeployService(context.Background(), spec); err != nil {
		t.Fatalf("deploy failed: %v", err)
	}

	// The service is monitored and healthy: a queued restart must not touch
	// a container that recovered in the meantime.
	health, ok := m.Health().GetServiceHealth("worker")
	if !ok || !health.IsHealthy {
		t.Fatalf("expected the service to be monitored and healthy")
	}

	// A crash spends the first restart attempt, so the health restart below
	// is deferred by the backoff.
	svc, _ := m.GetService("worker")
	fake.setRunning(svc.ContainerID, false, 1)
	m.reconcile(context.Background())
	if got := fake.startCount(); got != 1 {
		t.Fatalf("expected the crashed container to be started, got %d", got)
	}

	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}

	// The deferred restart runs once the backoff elapsed.
	time.Sleep(600 * time.Millisecond)

	if got := fake.restartCount(); got != 0 {
		t.Errorf("expected no restart for a recovered service, got %d", got)
	}

	// A skipped restart must not spend part of the restart budget.
	svc, _ = m.GetService("worker")
	if got := svc.RestartAttempts; got != 1 {
		t.Errorf("expected the skipped restart not to count as an attempt, got %d attempts", got)
	}
}

func TestManager_ReconcileRetriesStopOfExhaustedService(t *testing.T) {
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

	// Use up the restart budget so the service is marked failed.
	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}
	if err := m.onUnhealthy(context.Background(), "worker"); err != nil {
		t.Fatalf("onUnhealthy failed: %v", err)
	}
	svc, _ := m.GetService("worker")
	if svc.State != ServiceStateFailed {
		t.Fatalf("expected failed state, got %s", svc.State)
	}

	// The stop fails, so the service stays around with a stop intent.
	fake.mu.Lock()
	fake.stopErr = errors.New("daemon busy")
	fake.mu.Unlock()

	if err := m.StopService(context.Background(), "worker"); err == nil {
		t.Fatalf("expected the stop to fail")
	}
	if got := fake.stopCount(); got != 1 {
		t.Fatalf("expected 1 stop attempt, got %d", got)
	}

	// The reconcile loop must retry the stop of an exhausted service.
	fake.mu.Lock()
	fake.stopErr = nil
	fake.mu.Unlock()

	m.reconcile(context.Background())

	if got := fake.stopCount(); got != 2 {
		t.Errorf("expected the reconcile loop to retry the stop, got %d stop attempts", got)
	}
}
