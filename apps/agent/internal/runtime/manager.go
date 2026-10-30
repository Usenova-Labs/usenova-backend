package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/docker"
	"github.com/byoi-platform/agent/internal/health"
)

const (
	defaultReconcileInterval = 60 * time.Second

	// Ownership labels. These are reserved: caller-supplied labels can never
	// override them (see sanitizeLabels), so their presence is authoritative.
	labelManaged = "usenova.managed"
	labelService = "usenova.service"
	labelMeta    = "usenova.meta"
)

// reservedLabels are ownership markers that callers must not be able to spoof
// or overwrite via ServiceSpec.Labels.
var reservedLabels = map[string]bool{
	labelManaged: true,
	labelService: true,
	labelMeta:    true,
}

// errSuperseded is returned internally when a long-running operation discovers
// that a newer intent (stop/remove/redeploy) has replaced it.
var errSuperseded = errors.New("operation superseded")

// restartKind selects which container operation a restart attempt performs.
type restartKind int

const (
	// restartKindHealth restarts a running container that fails its health
	// checks.
	restartKindHealth restartKind = iota
	// restartKindStart starts a container that exited.
	restartKindStart
	// restartKindRedeploy re-provisions a container that is gone.
	restartKindRedeploy
)

// ErrServiceNotFound is returned when the named service is not managed by this
// agent. Callers (the local API) map it to 404 instead of a generic failure.
var ErrServiceNotFound = errors.New("service not found")

// ErrRuntimeUnavailable is returned when the container runtime cannot serve a
// request (no Docker client, or the daemon is unreachable). Callers map it to
// 503 so operators see "Docker is down", not "the agent is broken".
var ErrRuntimeUnavailable = errors.New("container runtime unavailable")

// serviceMeta is the non-secret deployment metadata persisted as a container
// label so a fresh manager can re-adopt containers after an agent restart.
// Secrets (env values, registry credentials) are deliberately excluded.
type serviceMeta struct {
	HealthCheck *docker.HealthConfig `json:"health_check,omitempty"`
	// Restart limits are persisted too, so a restarted agent keeps enforcing
	// the same policy for the services it re-adopts.
	MaxRestarts           int `json:"max_restarts,omitempty"`
	RestartWindowSeconds  int `json:"restart_window_seconds,omitempty"`
	RestartBackoffSeconds int `json:"restart_backoff_seconds,omitempty"`
}

// ContainerRuntime is the subset of the Docker wrapper the manager depends on.
// It is an interface so the runtime can be unit-tested without a daemon.
type ContainerRuntime interface {
	IsAvailable(ctx context.Context) bool
	PullImage(ctx context.Context, image string, authConfig *registry.AuthConfig) error
	RunContainer(ctx context.Context, cfg docker.ContainerConfig) (string, error)
	StartContainer(ctx context.Context, containerID string) error
	StopContainer(ctx context.Context, containerID string, timeout time.Duration) error
	RestartContainer(ctx context.Context, containerID string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, containerID string, force bool) error
	InspectContainer(ctx context.Context, containerID string) (*container.InspectResponse, error)
	ListContainers(ctx context.Context, all bool, filters client.Filters) ([]container.Summary, error)
	GetContainerLogs(ctx context.Context, containerID string, opts docker.LogOptions) (io.ReadCloser, error)
}

// Manager tracks desired services and keeps their containers alive.
// It is safe for concurrent use.
type Manager struct {
	docker  ContainerRuntime
	monitor *health.Monitor
	log     *logrus.Logger

	mu             sync.RWMutex
	services       map[string]*Service
	started        bool
	stopped        bool
	ctx            context.Context
	reconcileStop  chan struct{}
	reconcileDone  sync.WaitGroup
	reconcileEvery time.Duration

	// restartPolicy holds the agent-wide restart limits applied to services
	// that do not override them in their spec.
	restartPolicy RestartPolicy

	// stopCh is closed when the manager stops. Deferred restarts waiting out
	// a backoff abandon their wait instead of touching Docker after shutdown.
	stopCh   chan struct{}
	stopOnce sync.Once

	// scheduled tracks deferred restarts so Stop can wait for them: no
	// container-mutating work may outlive the manager.
	scheduled sync.WaitGroup

	// restartSeq numbers the deferred restarts so a service can tell which
	// queued attempt it is holding.
	restartSeq uint64

	// opLocks serialise container-mutating operations per service name. They
	// are separate from mu so long-running network work (image pulls) never
	// blocks short stop/restart operations or vice-versa.
	opMu    sync.Mutex
	opLocks map[string]*sync.Mutex
}

// NewManager creates a runtime manager backed by the given container runtime.
func NewManager(dc ContainerRuntime, log *logrus.Logger) *Manager {
	m := &Manager{
		docker:         dc,
		log:            log,
		services:       make(map[string]*Service),
		opLocks:        make(map[string]*sync.Mutex),
		reconcileEvery: defaultReconcileInterval,
		restartPolicy:  DefaultRestartPolicy(),
		stopCh:         make(chan struct{}),
	}

	// Health monitor restarts unhealthy services through our own handler.
	m.monitor = health.NewMonitor(log, m.onUnhealthy)
	m.monitor.SetRestartPolicy(RestartPolicyOnFailure)

	return m
}

// Start launches the health monitor and the reconciliation loop, and adopts
// any managed containers left over from a previous agent run.
// It is not an error for the Docker daemon to be unreachable right now:
// reconciliation will pick it up when it comes back.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return fmt.Errorf("runtime manager already started")
	}
	m.started = true
	m.ctx = ctx
	m.reconcileStop = make(chan struct{})
	m.mu.Unlock()

	if m.docker != nil && m.docker.IsAvailable(ctx) {
		m.adopt(ctx)
	} else {
		m.log.Warn("Docker daemon not available at startup; will keep retrying via reconcile loop")
	}

	m.monitor.Start(ctx)

	m.reconcileDone.Add(1)
	go m.reconcileLoop(ctx)

	m.log.Info("Runtime manager started")
	return nil
}

// Stop shuts down the reconcile loop and health monitors cleanly. It cancels
// all in-flight deploys and waits for monitor callbacks before returning, so
// no container-mutating work outlives the manager.
//
// Managed containers are intentionally left running: the Docker restart policy
// keeps them alive, and a future manager will re-adopt them on startup.
func (m *Manager) Stop(ctx context.Context) {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	m.started = false
	m.stopped = true
	stop := m.reconcileStop
	for _, svc := range m.services {
		if svc.cancel != nil {
			svc.cancel()
		}
	}
	m.mu.Unlock()

	if stop != nil {
		close(stop)
	}

	// Wake deferred restarts so they abandon their wait instead of running
	// against a stopped manager.
	m.stopOnce.Do(func() { close(m.stopCh) })

	done := make(chan struct{})
	go func() {
		m.reconcileDone.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		m.log.Warn("Runtime manager stop timed out waiting for reconcile loop")
	case <-time.After(10 * time.Second):
		m.log.Warn("Runtime manager reconcile loop did not stop within 10s")
	}

	m.monitor.Stop()

	// Deferred restarts must not outlive the manager either.
	scheduled := make(chan struct{})
	go func() {
		m.scheduled.Wait()
		close(scheduled)
	}()
	select {
	case <-scheduled:
	case <-ctx.Done():
		m.log.Warn("Runtime manager stop timed out waiting for deferred restarts")
	case <-time.After(10 * time.Second):
		m.log.Warn("Runtime manager deferred restarts did not stop within 10s")
	}

	m.log.Info("Runtime manager stopped")
}

// SetRestartPolicy sets the agent-wide restart limits. Services that do not
// override them in their spec inherit these values.
func (m *Manager) SetRestartPolicy(policy RestartPolicy) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if policy.MaxBackoff > 0 && policy.MaxBackoff < policy.Backoff {
		// Never cap the configured base backoff below itself.
		policy.MaxBackoff = policy.Backoff
	}
	m.restartPolicy = policy
}

// baseCtx returns the manager's long-lived context. Scheduled restarts use it
// because they run after the request that triggered them has returned.
func (m *Manager) baseCtx() context.Context {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.ctx != nil {
		return m.ctx
	}
	return context.Background()
}

// policyForSpec merges the agent-wide restart limits with the per-service
// overrides carried by the spec. A zero value in the spec means "inherit the
// agent default"; a negative max_restarts means "never limit restarts".
func (m *Manager) policyForSpec(spec docker.ServiceSpec) RestartPolicy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.policyForSpecLocked(spec)
}

// policyForSpecLocked is policyForSpec for callers that already hold the
// manager lock.
func (m *Manager) policyForSpecLocked(spec docker.ServiceSpec) RestartPolicy {
	policy := m.restartPolicy

	if spec.MaxRestarts != 0 {
		policy.MaxRestarts = spec.MaxRestarts
	}
	if spec.RestartWindowSeconds > 0 {
		policy.Window = time.Duration(spec.RestartWindowSeconds) * time.Second
	}
	if spec.RestartBackoffSeconds > 0 {
		policy.Backoff = time.Duration(spec.RestartBackoffSeconds) * time.Second
		if policy.MaxBackoff > 0 && policy.MaxBackoff < policy.Backoff {
			// Never clamp the configured base backoff below itself.
			policy.MaxBackoff = policy.Backoff
		}
	}
	return policy
}

// DeployService pulls the image, creates and starts the container, and
// registers health monitoring.
func (m *Manager) DeployService(ctx context.Context, spec docker.ServiceSpec) (*Service, error) {
	if spec.Name == "" {
		return nil, fmt.Errorf("service name is required")
	}
	if spec.Image == "" {
		return nil, fmt.Errorf("service image is required")
	}
	if m.docker == nil {
		return nil, fmt.Errorf("docker client not initialized")
	}

	// Resolve the restart limits before taking the lock: a redeploy starts a
	// fresh restart budget for the new incarnation.
	restartPolicy := m.policyForSpec(spec)

	m.mu.Lock()
	existing := m.services[spec.Name]
	if existing != nil && existing.DesiredState == ServiceStateRunning && existing.State == ServiceStateRunning {
		m.mu.Unlock()
		return nil, fmt.Errorf("service %s is already running (stop it first to redeploy)", spec.Name)
	}
	// Fence any in-flight operation on the previous incarnation.
	if existing != nil && existing.cancel != nil {
		existing.cancel()
	}

	base := m.ctx
	if base == nil {
		base = context.Background()
	}
	opCtx, cancel := context.WithCancel(base)

	svc := &Service{
		Spec:         spec,
		State:        ServiceStatePending,
		DesiredState: ServiceStateRunning,
		cancel:       cancel,
		restart:      newRestartBreaker(restartPolicy),
	}
	if existing != nil {
		svc.generation = existing.generation + 1
	} else {
		svc.generation = 1
	}
	m.services[spec.Name] = svc
	gen := svc.generation
	m.mu.Unlock()

	if err := m.provision(opCtx, svc, gen); err != nil {
		if errors.Is(err, errSuperseded) {
			return nil, fmt.Errorf("deploy of service %s was superseded", spec.Name)
		}
		m.failService(spec.Name, svc, gen, err)
		return nil, err
	}

	deployed, ok := m.snapshot(spec.Name)
	if !ok {
		return nil, fmt.Errorf("service %s disappeared after deploy", spec.Name)
	}
	return deployed, nil
}

// provision performs the pull + replace + run + commit sequence for a service.
// It only commits if the service is still the current incarnation and still
// desired running, which fences concurrent stop/remove.
func (m *Manager) provision(ctx context.Context, svc *Service, gen uint64) error {
	spec := svc.Spec

	// Pull image outside the op lock: this can take a long time and must not
	// block stop/remove. TODO: image pull policy (always/if-not-present/never)
	// and private registry auth.
	if err := m.docker.PullImage(ctx, spec.Image, nil); err != nil {
		if !m.current(spec.Name, svc, gen) {
			return errSuperseded
		}
		return fmt.Errorf("pull image: %w", err)
	}
	if !m.current(spec.Name, svc, gen) {
		return errSuperseded
	}

	lock := m.opLock(spec.Name)
	lock.Lock()
	defer lock.Unlock()

	if !m.current(spec.Name, svc, gen) {
		return errSuperseded
	}

	// Remove any existing managed container for this service (stale container
	// from a previous run, or the previous container during a redeploy).
	if existingID, err := m.findManagedContainer(ctx, spec.Name); err != nil {
		return fmt.Errorf("look up existing container: %w", err)
	} else if existingID != "" {
		m.log.WithFields(logrus.Fields{
			"service": spec.Name,
			"id":      shortID(existingID),
		}).Info("Removing stale container before deploy")
		if err := m.docker.RemoveContainer(ctx, existingID, true); err != nil {
			if !m.current(spec.Name, svc, gen) {
				return errSuperseded
			}
			return fmt.Errorf("remove stale container: %w", err)
		}
	}

	id, err := m.docker.RunContainer(ctx, buildContainerConfig(spec))
	if err != nil {
		if !m.current(spec.Name, svc, gen) || ctx.Err() != nil {
			return errSuperseded
		}
		return fmt.Errorf("run container: %w", err)
	}

	// Discover the ports Docker actually published for health probes.
	var published map[int]int
	if info, ierr := m.docker.InspectContainer(ctx, id); ierr == nil {
		published = publishedPorts(info)
	} else {
		m.log.WithError(ierr).Debug("Could not inspect container for published ports; falling back to spec ports")
	}

	m.mu.Lock()
	if m.services[spec.Name] != svc || svc.generation != gen || svc.DesiredState != ServiceStateRunning {
		m.mu.Unlock()
		m.removeContainerQuietly(id)
		return errSuperseded
	}
	svc.ContainerID = id
	svc.State = ServiceStateRunning
	svc.DeployedAt = time.Now()
	svc.LastError = nil
	hasHealth := false
	if hc := buildHealthCheck(spec, published); hc != nil {
		svc.Health = hc
		hasHealth = true
		m.monitor.AddService(spec.Name, *hc)
	}
	m.mu.Unlock()

	m.log.WithFields(logrus.Fields{
		"service":    spec.Name,
		"id":         shortID(id),
		"image":      spec.Image,
		"has_health": hasHealth,
	}).Info("Service deployed")

	return nil
}

// StopService stops the container for a service but keeps its desired spec.
func (m *Manager) StopService(ctx context.Context, name string) error {
	m.mu.Lock()
	svc, ok := m.services[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("service %s not found", name)
	}
	// Bump generation / cancel in-flight deploy first so a concurrent deploy
	// cannot commit after this stop.
	if svc.cancel != nil {
		svc.cancel()
	}
	svc.generation++
	gen := svc.generation
	svc.DesiredState = ServiceStateStopped
	id := svc.ContainerID
	m.mu.Unlock()

	// Stop health restarts immediately; monitoring a stopped service is wrong.
	m.monitor.RemoveService(name)

	if id != "" {
		lock := m.opLock(name)
		lock.Lock()
		err := m.docker.StopContainer(ctx, id, 30*time.Second)
		lock.Unlock()
		if err != nil {
			m.mu.Lock()
			if m.services[name] == svc && svc.generation == gen {
				svc.LastError = err
			}
			m.mu.Unlock()
			return fmt.Errorf("stop service %s: %w", name, err)
		}
	}

	m.mu.Lock()
	if m.services[name] == svc && svc.generation == gen {
		svc.State = ServiceStateStopped
		svc.LastError = nil
		// An intentional stop ends the current restart budget: the service
		// can be started again without being marked failed.
		svc.restartExhausted = false
		if svc.restart != nil {
			svc.restart.reset()
			svc.RestartAttempts = 0
		}
	}
	m.mu.Unlock()

	m.log.WithField("service", name).Info("Service stopped")
	return nil
}

// RemoveService removes the container and forgets the service. Ownership is
// retained (with ServiceStateRemoved) until Docker removal actually succeeds,
// so a failed removal cannot leave an untracked, published container behind.
func (m *Manager) RemoveService(ctx context.Context, name string) error {
	m.mu.Lock()
	svc, ok := m.services[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("service %s not found", name)
	}
	if svc.cancel != nil {
		svc.cancel()
	}
	svc.generation++
	gen := svc.generation
	svc.DesiredState = ServiceStateRemoved
	id := svc.ContainerID
	m.mu.Unlock()

	// Stop health restarts immediately.
	m.monitor.RemoveService(name)

	lock := m.opLock(name)
	lock.Lock()
	defer lock.Unlock()

	if id != "" {
		if err := m.docker.RemoveContainer(ctx, id, true); err != nil {
			// Keep ownership so the container stays tracked and retryable.
			m.mu.Lock()
			if m.services[name] == svc && svc.generation == gen {
				svc.State = ServiceStateFailed
				svc.LastError = err
			}
			m.mu.Unlock()
			return fmt.Errorf("remove service %s container: %w", name, err)
		}
	}

	m.mu.Lock()
	if m.services[name] == svc && svc.generation == gen {
		delete(m.services, name)
	}
	m.mu.Unlock()

	m.log.WithField("service", name).Info("Service removed")
	return nil
}

// RunningCount returns the number of services believed to be running.
// The reconcile loop keeps these states fresh; a cheap heartbeat-safe count.
// TODO: optionally verify liveness with a docker inspect and cache the result.
func (m *Manager) RunningCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, svc := range m.services {
		if svc.State == ServiceStateRunning {
			count++
		}
	}
	return count
}

// FailedCount returns the number of services the agent gave up on (they
// exhausted their restart budget or their deploy failed). It is a cheap
// heartbeat-safe count of services that need operator attention.
func (m *Manager) FailedCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, svc := range m.services {
		if svc.State == ServiceStateFailed {
			count++
		}
	}
	return count
}

// GetService returns a snapshot of a managed service by name.
func (m *Manager) GetService(name string) (*Service, bool) {
	return m.snapshot(name)
}

// ServiceLogs returns a stream of a managed service's container logs. Like the
// other mutating operations it resolves the container under the per-service
// lock so a concurrent stop/redeploy cannot make it read a container that is
// about to be replaced. The caller must close the returned reader.
//
// It distinguishes "no such service" (ErrServiceNotFound), "cannot serve logs
// right now" (ErrRuntimeUnavailable) and "service has no container yet"
// (ErrServiceNotFound) so the HTTP layer can map them to clear status codes.
func (m *Manager) ServiceLogs(ctx context.Context, name string, opts docker.LogOptions) (io.ReadCloser, error) {
	// Cheap existence check so an unknown service is rejected without taking
	// the op lock. The container ID is re-read under the lock below, where a
	// concurrent redeploy cannot leave us holding a stale one.
	m.mu.RLock()
	_, ok := m.services[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, name)
	}

	if m.docker == nil || !m.docker.IsAvailable(ctx) {
		return nil, fmt.Errorf("%w: Docker daemon is not reachable", ErrRuntimeUnavailable)
	}

	lock := m.opLock(name)
	lock.Lock()
	defer lock.Unlock()

	// Read the container ID under the op lock: a redeploy that held the lock
	// before us has already published the new ContainerID, so we never read a
	// container that is about to be replaced.
	m.mu.RLock()
	svc, still := m.services[name]
	var id string
	if still {
		id = svc.ContainerID
	}
	m.mu.RUnlock()
	if !still {
		// The service was removed while we waited for the lock.
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, name)
	}

	if id == "" {
		// The service may have been adopted or deployed since the snapshot;
		// fall back to a label-scoped lookup before giving up.
		found, err := m.findManagedContainer(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("look up container for %s: %w", name, err)
		}
		if found == "" {
			return nil, fmt.Errorf("%w: service %s has no container", ErrServiceNotFound, name)
		}
		id = found
	}

	rc, err := m.docker.GetContainerLogs(ctx, id, opts)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, fmt.Errorf("%w: container for service %s is gone", ErrServiceNotFound, name)
		}
		return nil, err
	}
	return rc, nil
}

// ListServices returns snapshots of all managed services.
func (m *Manager) ListServices() []*Service {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Service, 0, len(m.services))
	now := time.Now()
	for _, svc := range m.services {
		c := *svc
		m.annotateRestartStatusLocked(&c, now)
		out = append(out, &c)
	}
	return out
}

// Health returns the underlying health monitor (for status reporting).
func (m *Manager) Health() *health.Monitor {
	return m.monitor
}

// onUnhealthy is invoked by the health monitor when a service fails its
// checks. Restarts the container according to the service restart policy,
// bounded by the service's restart breaker.
func (m *Manager) onUnhealthy(ctx context.Context, name string) error {
	m.mu.RLock()
	svc, ok := m.services[name]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("unhealthy callback for unknown service %s", name)
	}
	policy := svc.Spec.RestartPolicy
	desired := svc.DesiredState
	state := svc.State
	gen := svc.generation
	m.mu.RUnlock()

	if desired != ServiceStateRunning || state == ServiceStateStopped {
		m.log.WithField("service", name).Debug("Ignoring unhealthy signal for non-running service")
		return nil
	}

	if !shouldRestart(policy) {
		m.log.WithFields(logrus.Fields{
			"service": name,
			"policy":  policy,
		}).Warn("Service unhealthy but restart policy forbids restart")
		m.updateIfCurrent(name, svc, gen, func(s *Service) { s.State = ServiceStateUnhealthy })
		return nil
	}

	return m.attemptRestart(ctx, name, svc, gen, restartKindHealth)
}

// attemptRestart centralises every agent-initiated restart. The service's
// restart breaker decides whether the attempt is allowed: it caps the number
// of restarts per window and applies a backoff between attempts.
//
// A deferred attempt is scheduled instead of being dropped, so a service that
// is flapping still gets its restart once the backoff elapsed. When the
// breaker refuses the attempt the service is marked failed and health
// monitoring stops, so the agent no longer touches it.
func (m *Manager) attemptRestart(ctx context.Context, name string, svc *Service, gen uint64, kind restartKind) error {
	decision, ok := m.permitRestart(name, svc, gen)
	if !ok {
		// The service was stopped, removed or redeployed meanwhile.
		return nil
	}

	if !decision.Allowed {
		m.failRestartLimit(name, svc, gen, decision)
		return nil
	}

	if decision.Deferred {
		m.scheduleRestart(name, svc, gen, decision.NextAttemptAt, kind)
		return nil
	}

	m.recordRestartAttempt(name, svc, gen)
	return m.performRestart(ctx, name, svc, gen, kind)
}

// permitRestart asks the service's restart breaker whether a restart attempt
// may be made now. ok is false when the service is no longer the current
// incarnation, in which case the caller must not touch it.
func (m *Manager) permitRestart(name string, svc *Service, gen uint64) (restartDecision, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isSameLocked(name, svc, gen) {
		return restartDecision{}, false
	}
	if svc.restart == nil {
		svc.restart = newRestartBreaker(m.policyForSpecLocked(svc.Spec))
	}
	decision := svc.restart.decide(time.Now())
	svc.RestartAttempts = decision.Attempts
	return decision, true
}

// recordRestartAttempt registers a restart attempt with the service's
// breaker and refreshes the counters exposed in the service status.
func (m *Manager) recordRestartAttempt(name string, svc *Service, gen uint64) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isSameLocked(name, svc, gen) {
		return
	}
	if svc.restart == nil {
		svc.restart = newRestartBreaker(m.policyForSpecLocked(svc.Spec))
	}
	svc.restart.record(now)
	svc.RestartAttempts = svc.restart.count(now)
	svc.LastRestartAt = now
}

// scheduleRestart arranges for a deferred restart attempt to run once the
// backoff has elapsed. The attempt is re-evaluated when it runs, so a service
// that exceeded its restart limit in the meantime is failed instead of
// restarted.
//
// Only one deferred restart is queued per service: a second unhealthy
// transition (or a reconcile pass) must not stack up another attempt that
// would restart the container again at the next backoff boundary.
func (m *Manager) scheduleRestart(name string, svc *Service, gen uint64, at time.Time, kind restartKind) {
	m.mu.Lock()
	if m.stopped || !m.isSameLocked(name, svc, gen) {
		m.mu.Unlock()
		return
	}
	if svc.restartPendingSeq != 0 {
		// A deferred restart is already queued for this service.
		m.mu.Unlock()
		m.log.WithField("service", name).Debug("Restart already deferred; not queueing another one")
		return
	}
	m.restartSeq++
	seq := m.restartSeq
	svc.restartPendingSeq = seq
	m.scheduled.Add(1)
	m.mu.Unlock()

	// The attempt outlives the caller (the health callback context is short
	// lived), so it runs on the manager's context instead.
	ctx := m.baseCtx()

	m.log.WithFields(logrus.Fields{
		"service": name,
		"at":      at.Format(time.RFC3339),
	}).Info("Restart deferred by backoff")

	go func() {
		defer m.scheduled.Done()
		defer m.clearRestartPending(name, svc, gen, seq)

		timer := time.NewTimer(time.Until(at))
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		}

		// Release the slot before the attempt so an attempt that is deferred
		// again can queue itself.
		m.clearRestartPending(name, svc, gen, seq)

		// The service may have recovered while the backoff was running.
		// Restarting it now would be harmful, and it must not spend part of
		// the restart budget either.
		if kind == restartKindHealth && m.serviceRecovered(name) {
			m.log.WithField("service", name).Info("Service recovered; skipping deferred restart")
			return
		}

		m.log.WithFields(logrus.Fields{
			"service": name,
			"at":      at.Format(time.RFC3339),
		}).Info("Deferred restart is due; running it now")

		if err := m.attemptRestart(ctx, name, svc, gen, kind); err != nil {
			m.log.WithError(err).WithField("service", name).Warn("Deferred restart failed")
		}
	}()
}

// clearRestartPending releases the deferred restart slot of a service. The
// sequence number makes it a no-op when a newer attempt already took the slot.
func (m *Manager) clearRestartPending(name string, svc *Service, gen uint64, seq uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isSameLocked(name, svc, gen) {
		return
	}
	if svc.restartPendingSeq == seq {
		svc.restartPendingSeq = 0
	}
}

// serviceRecovered reports whether the health monitor considers the service
// healthy again. A service that is not monitored is never considered
// recovered.
func (m *Manager) serviceRecovered(name string) bool {
	h, ok := m.monitor.GetServiceHealth(name)
	return ok && h.IsHealthy
}

// performRestart performs the container operation of an allowed restart
// attempt. The service currency is re-checked under the operation lock so a
// concurrent stop/remove/redeploy wins over a stale restart.
func (m *Manager) performRestart(ctx context.Context, name string, svc *Service, gen uint64, kind restartKind) error {
	switch kind {
	case restartKindHealth:
		return m.restartContainer(ctx, name, svc, gen)
	case restartKindStart:
		return m.startContainer(ctx, name, svc, gen)
	case restartKindRedeploy:
		m.redeploy(ctx, name, svc, gen)
		return nil
	default:
		return nil
	}
}

// restartContainer restarts a running container that failed its health
// checks.
func (m *Manager) restartContainer(ctx context.Context, name string, svc *Service, gen uint64) error {
	lock := m.opLock(name)
	lock.Lock()
	defer lock.Unlock()

	// Re-validate after acquiring the op lock: intent may have changed while
	// we waited (stop/remove must win).
	m.mu.RLock()
	if m.services[name] != svc || svc.generation != gen || svc.DesiredState != ServiceStateRunning {
		m.mu.RUnlock()
		return nil
	}
	id := svc.ContainerID
	policy := svc.Spec.RestartPolicy
	m.mu.RUnlock()

	if id == "" {
		return fmt.Errorf("service %s has no container to restart", name)
	}

	m.log.WithFields(logrus.Fields{
		"service": name,
		"id":      shortID(id),
		"policy":  policy,
	}).Warn("Restarting unhealthy service")

	if err := m.docker.RestartContainer(ctx, id, 30*time.Second); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("restart service %s: %w", name, err)
	}

	m.updateIfCurrent(name, svc, gen, func(s *Service) {
		s.RestartCount++
		s.State = ServiceStateRunning
		s.LastError = nil
	})

	return nil
}

// startContainer starts an exited container. A container that cannot be
// started is re-provisioned from its spec.
func (m *Manager) startContainer(ctx context.Context, name string, svc *Service, gen uint64) error {
	lock := m.opLock(name)
	lock.Lock()
	if !m.current(name, svc, gen) {
		lock.Unlock()
		return nil
	}
	id := svc.ContainerID
	err := m.docker.StartContainer(ctx, id)
	lock.Unlock()

	if err != nil {
		m.log.WithError(err).WithField("service", name).Warn("Start failed; redeploying")
		m.redeploy(ctx, name, svc, gen)
		return nil
	}

	m.updateIfCurrent(name, svc, gen, func(s *Service) {
		s.RestartCount++
		s.State = ServiceStateRunning
	})
	return nil
}

// failRestartLimit marks a service as failed because it exhausted its restart
// budget and stops health monitoring, so the agent leaves it alone until an
// operator deploys it again.
func (m *Manager) failRestartLimit(name string, svc *Service, gen uint64, decision restartDecision) {
	err := fmt.Errorf("restart limit reached: %s", decision.Reason)
	if !m.updateIfCurrent(name, svc, gen, func(s *Service) {
		s.State = ServiceStateFailed
		s.LastError = err
		s.restartExhausted = true
	}) {
		return
	}
	m.monitor.RemoveService(name)
	m.log.WithFields(logrus.Fields{
		"service":  name,
		"attempts": decision.Attempts,
	}).Error("Service exceeded its restart limit; not restarting it again")
}

// reconcileLoop periodically inspects managed containers and corrects drift.
func (m *Manager) reconcileLoop(ctx context.Context) {
	defer m.reconcileDone.Done()

	ticker := time.NewTicker(m.reconcileEvery)
	defer ticker.Stop()

	m.log.WithField("interval", m.reconcileEvery).Debug("Reconcile loop started")

	for {
		select {
		case <-ticker.C:
			m.reconcile(ctx)
		case <-m.reconcileStop:
			m.log.Debug("Reconcile loop stopping")
			return
		case <-ctx.Done():
			m.log.Debug("Reconcile loop stopping due to context cancellation")
			return
		}
	}
}

// reconcile inspects every managed container and corrects drift.
func (m *Manager) reconcile(ctx context.Context) {
	if m.docker == nil {
		return
	}
	if !m.docker.IsAvailable(ctx) {
		return
	}

	m.mu.RLock()
	type item struct {
		name string
		svc  *Service
		gen  uint64
	}
	items := make([]item, 0, len(m.services))
	for name, svc := range m.services {
		items = append(items, item{name, svc, svc.generation})
	}
	m.mu.RUnlock()

	for _, it := range items {
		m.reconcileService(ctx, it.name, it.svc, it.gen)
	}
}

// reconcileService drives one service toward its desired state.
func (m *Manager) reconcileService(ctx context.Context, name string, svc *Service, gen uint64) {
	// Snapshot intent. Re-checked under the op lock before mutating.
	m.mu.RLock()
	if m.services[name] != svc || svc.generation != gen {
		m.mu.RUnlock()
		return
	}
	desired := svc.DesiredState
	id := svc.ContainerID
	policy := svc.Spec.RestartPolicy
	exhausted := svc.restartExhausted
	m.mu.RUnlock()

	switch desired {
	case ServiceStateRemoved:
		m.reconcileRemoval(ctx, name, svc, gen, id)
		return
	case ServiceStateStopped:
		m.reconcileStopped(ctx, name, svc, gen, id)
		return
	}

	if exhausted {
		// The service used up its restart budget: it stays failed until it
		// is deployed again. Stop and removal intents are handled above, so
		// a failed stop or removal can still be retried.
		return
	}

	// desired == running
	if id == "" {
		if shouldRestart(policy) {
			m.attemptRestart(ctx, name, svc, gen, restartKindRedeploy)
		}
		return
	}

	info, err := m.docker.InspectContainer(ctx, id)
	if err != nil {
		if shouldRestart(policy) {
			m.log.WithFields(logrus.Fields{
				"service": name,
				"error":   err.Error(),
			}).Warn("Managed container missing; redeploying")
			m.attemptRestart(ctx, name, svc, gen, restartKindRedeploy)
		} else {
			m.updateIfCurrent(name, svc, gen, func(s *Service) {
				s.State = ServiceStateFailed
				s.LastError = err
			})
		}
		return
	}

	if info.State != nil && info.State.Running {
		m.updateIfCurrent(name, svc, gen, func(s *Service) { s.State = ServiceStateRunning })
		return
	}

	exitCode := 0
	if info.State != nil {
		exitCode = info.State.ExitCode
	}
	restartable := policy == RestartPolicyAlways ||
		policy == RestartPolicyUnlessStopped ||
		(policy == RestartPolicyOnFailure && exitCode != 0)

	if !restartable {
		m.updateIfCurrent(name, svc, gen, func(s *Service) {
			s.State = ServiceStateFailed
			s.LastError = fmt.Errorf("container exited with code %d", exitCode)
		})
		return
	}

	m.log.WithFields(logrus.Fields{
		"service":   name,
		"exit_code": exitCode,
		"policy":    policy,
	}).Warn("Managed container exited; restarting")
	m.attemptRestart(ctx, name, svc, gen, restartKindStart)
}

// reconcileRemoval retries removal until the container is actually gone.
func (m *Manager) reconcileRemoval(ctx context.Context, name string, svc *Service, gen uint64, id string) {
	if id != "" {
		lock := m.opLock(name)
		lock.Lock()
		err := error(nil)
		if m.isSame(name, svc, gen) {
			err = m.docker.RemoveContainer(ctx, id, true)
		}
		lock.Unlock()
		if err != nil {
			m.updateIfCurrent(name, svc, gen, func(s *Service) {
				s.State = ServiceStateFailed
				s.LastError = err
			})
			return
		}
	}
	if m.removeIfCurrent(name, svc, gen) {
		m.log.WithField("service", name).Info("Removed service after retry")
	}
}

// reconcileStopped enforces stop intent if the container was restarted
// externally.
func (m *Manager) reconcileStopped(ctx context.Context, name string, svc *Service, gen uint64, id string) {
	if id == "" {
		return
	}
	lock := m.opLock(name)
	lock.Lock()
	defer lock.Unlock()
	if !m.isSame(name, svc, gen) {
		return
	}
	info, err := m.docker.InspectContainer(ctx, id)
	if err != nil || info.State == nil || !info.State.Running {
		return
	}
	if err := m.docker.StopContainer(ctx, id, 30*time.Second); err != nil {
		m.log.WithError(err).WithField("service", name).Warn("Failed to enforce stop intent")
	}
}

// redeploy re-provisions a service from its desired spec using the same
// generation (it does not change intent).
func (m *Manager) redeploy(ctx context.Context, name string, svc *Service, gen uint64) {
	if !m.current(name, svc, gen) {
		return
	}
	if err := m.provision(ctx, svc, gen); err != nil {
		if errors.Is(err, errSuperseded) {
			return
		}
		m.failService(name, svc, gen, err)
	}
}

// failService marks a service as failed, unless it has been superseded.
func (m *Manager) failService(name string, svc *Service, gen uint64, err error) {
	if !m.updateIfCurrent(name, svc, gen, func(s *Service) {
		s.State = ServiceStateFailed
		s.LastError = err
	}) {
		return
	}
	m.monitor.RemoveService(name)
	m.log.WithFields(logrus.Fields{"service": name, "error": err.Error()}).Error("Service deployment failed")
}

// current reports whether svc is still the current incarnation and still
// desired running.
func (m *Manager) current(name string, svc *Service, gen uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cur, ok := m.services[name]
	return ok && cur == svc && cur.generation == gen && svc.DesiredState == ServiceStateRunning
}

// isSame reports whether svc is still the current incarnation (regardless of
// desired state).
func (m *Manager) isSame(name string, svc *Service, gen uint64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cur, ok := m.services[name]
	return ok && cur == svc && cur.generation == gen
}

// updateIfCurrent applies fn only if svc is still the current incarnation.
// Returns true when applied.
func (m *Manager) updateIfCurrent(name string, svc *Service, gen uint64, fn func(*Service)) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isSameLocked(name, svc, gen) {
		return false
	}
	fn(svc)
	return true
}

// removeIfCurrent deletes ownership only if svc is still the current
// incarnation. Returns true when deleted.
func (m *Manager) removeIfCurrent(name string, svc *Service, gen uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.isSameLocked(name, svc, gen) {
		return false
	}
	delete(m.services, name)
	return true
}

func (m *Manager) isSameLocked(name string, svc *Service, gen uint64) bool {
	cur, ok := m.services[name]
	return ok && cur == svc && cur.generation == gen
}

// opLock returns the per-service mutex used to serialise container mutations.
func (m *Manager) opLock(name string) *sync.Mutex {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	l, ok := m.opLocks[name]
	if !ok {
		l = &sync.Mutex{}
		m.opLocks[name] = l
	}
	return l
}

// removeContainerQuietly best-effort removes a container we decided not to keep
// (e.g. a deploy that lost its generation race).
func (m *Manager) removeContainerQuietly(id string) {
	if id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.docker.RemoveContainer(ctx, id, true); err != nil {
		m.log.WithError(err).WithField("id", shortID(id)).Warn("Failed to roll back superseded container")
	}
}

// adopt reconstructs ownership of managed containers found on the daemon. This
// is what keeps services monitored and countable across agent restarts.
func (m *Manager) adopt(ctx context.Context) {
	args := client.Filters{}.Add("label", labelManaged+"=true")
	containers, err := m.docker.ListContainers(ctx, true, args)
	if err != nil {
		m.log.WithError(err).Warn("Failed to list managed containers for adoption")
		return
	}

	adopted := 0
	for _, ctr := range containers {
		// Trust only containers that carry our authoritative ownership marker.
		if ctr.Labels[labelManaged] != "true" {
			continue
		}
		name := ctr.Labels[labelService]
		if name == "" {
			m.log.WithField("id", shortID(ctr.ID)).Warn("Skipping managed container without service label")
			continue
		}

		info, err := m.docker.InspectContainer(ctx, ctr.ID)
		if err != nil {
			m.log.WithError(err).WithField("id", shortID(ctr.ID)).Warn("Failed to inspect container for adoption")
			continue
		}

		spec := specFromInspect(name, info, ctr.Labels[labelMeta])
		state := ServiceStateStopped
		desired := ServiceStateStopped
		if info.State != nil && info.State.Running {
			state = ServiceStateRunning
			desired = ServiceStateRunning
		} else if shouldRestart(spec.RestartPolicy) {
			// Policy says keep it alive, so reconciliation should restart it.
			desired = ServiceStateRunning
		}

		svc := &Service{
			Spec:         spec,
			ContainerID:  ctr.ID,
			State:        state,
			DesiredState: desired,
			generation:   1,
			restart:      newRestartBreaker(m.policyForSpec(spec)),
		}
		if info.State != nil {
			if t, perr := time.Parse(time.RFC3339Nano, info.State.StartedAt); perr == nil {
				svc.DeployedAt = t
			}
		}

		m.mu.Lock()
		if _, exists := m.services[name]; exists {
			m.mu.Unlock()
			continue
		}
		m.services[name] = svc
		m.mu.Unlock()

		// Register health monitoring (monitor will start workers on Start).
		if hc := buildHealthCheck(spec, publishedPorts(info)); hc != nil {
			svc.Health = hc
			m.monitor.AddService(name, *hc)
		}

		adopted++
		m.log.WithFields(logrus.Fields{
			"service": name,
			"id":      shortID(ctr.ID),
			"state":   state,
		}).Info("Adopted existing managed container")
	}

	if adopted > 0 {
		m.log.WithField("count", adopted).Info("Adopted managed containers after restart")
	}
}

// findManagedContainer looks up a managed container for a service, scoped to
// our ownership labels so unrelated containers are never touched.
func (m *Manager) findManagedContainer(ctx context.Context, name string) (string, error) {
	args := client.Filters{}.
		Add("label", labelManaged+"=true").
		Add("label", labelService+"="+name)
	containers, err := m.docker.ListContainers(ctx, true, args)
	if err != nil {
		return "", err
	}
	for _, ctr := range containers {
		// Defensive client-side re-check in case the daemon ignored filters.
		if ctr.Labels[labelManaged] != "true" || ctr.Labels[labelService] != name {
			continue
		}
		return ctr.ID, nil
	}
	return "", nil
}

// specFromInspect reconstructs a desired spec from a running container so it can
// be re-adopted. Secrets are read back from the container's own config (which is
// where they already live), never from labels.
func specFromInspect(name string, info *container.InspectResponse, metaRaw string) docker.ServiceSpec {
	spec := docker.ServiceSpec{Name: name}
	if info == nil {
		return spec
	}

	if info.Config != nil {
		spec.Image = info.Config.Image
		spec.Env = append([]string(nil), info.Config.Env...)
	}
	if info.HostConfig != nil {
		spec.RestartPolicy = string(info.HostConfig.RestartPolicy.Name)
		spec.Volumes = append([]string(nil), info.HostConfig.Binds...)
		if info.HostConfig.NanoCPUs > 0 || info.HostConfig.Memory > 0 {
			spec.Resources = &docker.ResourceLimits{
				CPULimit:    info.HostConfig.NanoCPUs,
				MemoryLimit: info.HostConfig.Memory,
			}
		}
		if len(info.HostConfig.PortBindings) > 0 {
			spec.Ports = make(map[string]string, len(info.HostConfig.PortBindings))
			for port, bindings := range info.HostConfig.PortBindings {
				if len(bindings) == 0 {
					continue
				}
				spec.Ports[bindings[0].HostPort] = port.Port()
			}
		}
	}

	if metaRaw != "" {
		var meta serviceMeta
		if err := json.Unmarshal([]byte(metaRaw), &meta); err == nil {
			spec.HealthCheck = meta.HealthCheck
			spec.MaxRestarts = meta.MaxRestarts
			spec.RestartWindowSeconds = meta.RestartWindowSeconds
			spec.RestartBackoffSeconds = meta.RestartBackoffSeconds
		}
	}

	// Carry forward user labels, minus reserved ownership markers.
	if info.Config != nil && len(info.Config.Labels) > 0 {
		labels := make(map[string]string)
		for k, v := range info.Config.Labels {
			if reservedLabels[k] {
				continue
			}
			labels[k] = v
		}
		if len(labels) > 0 {
			spec.Labels = labels
		}
	}

	return spec
}

// buildContainerConfig maps a ServiceSpec to a docker ContainerConfig.
func buildContainerConfig(spec docker.ServiceSpec) docker.ContainerConfig {
	policy := spec.RestartPolicy
	if policy == "" {
		policy = RestartPolicyOnFailure
	}

	cfg := docker.ContainerConfig{
		Image:         spec.Image,
		Name:          containerName(spec.Name),
		Env:           spec.Env,
		Ports:         spec.Ports,
		Volumes:       spec.Volumes,
		RestartPolicy: policy,
		Labels:        managedLabels(spec),
	}
	if spec.Resources != nil {
		cfg.NanoCPUs = spec.Resources.CPULimit
		cfg.Memory = spec.Resources.MemoryLimit
	}
	return cfg
}

// managedLabels returns the authoritative ownership labels merged with the
// caller's labels. Reserved labels always win, so a caller cannot spoof or
// overwrite ownership markers.
func managedLabels(spec docker.ServiceSpec) map[string]string {
	labels := map[string]string{
		labelManaged: "true",
		labelService: spec.Name,
	}
	meta := serviceMeta{
		HealthCheck:           spec.HealthCheck,
		MaxRestarts:           spec.MaxRestarts,
		RestartWindowSeconds:  spec.RestartWindowSeconds,
		RestartBackoffSeconds: spec.RestartBackoffSeconds,
	}
	if encoded, err := json.Marshal(meta); err == nil {
		labels[labelMeta] = string(encoded)
	}
	for k, v := range spec.Labels {
		if reservedLabels[k] {
			continue
		}
		labels[k] = v
	}
	return labels
}

// buildHealthCheck derives an agent-side health check from the service spec.
// published maps container ports to the host ports Docker actually published;
// when non-empty it takes precedence over the spec. Returns nil when no health
// check is configured or no target port can be resolved.
func buildHealthCheck(spec docker.ServiceSpec, published map[int]int) *health.CheckConfig {
	hc := spec.HealthCheck
	if hc == nil {
		return nil
	}

	ports := published
	if len(ports) == 0 {
		ports = derivePorts(spec.Ports)
	}

	interval := hc.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	timeout := hc.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	retries := hc.Retries
	if retries <= 0 {
		retries = 3
	}

	cfg := &health.CheckConfig{
		Timeout:          timeout,
		Interval:         interval,
		FailureThreshold: retries,
		SuccessThreshold: 1,
	}

	switch hc.Type {
	case "http":
		hostPort := pickHostPort(ports, hc.Port)
		if hostPort == 0 {
			return nil
		}
		path := hc.Path
		if path == "" {
			path = "/health"
		}
		// TODO: configurable probe host (e.g. container IP on a docker network
		// instead of the published host port).
		cfg.Type = health.HTTP
		cfg.Endpoint = fmt.Sprintf("http://127.0.0.1:%d%s", hostPort, path)
	case "tcp":
		hostPort := pickHostPort(ports, hc.Port)
		if hostPort == 0 {
			return nil
		}
		cfg.Type = health.TCP
		cfg.Host = "127.0.0.1"
		cfg.Port = hostPort
	default:
		return nil
	}

	return cfg
}

// derivePorts converts spec ports ("hostPort" -> "containerPort") into a
// containerPort -> hostPort map.
func derivePorts(specPorts map[string]string) map[int]int {
	out := make(map[int]int, len(specPorts))
	for host, container := range specPorts {
		h, err1 := strconv.Atoi(host)
		c, err2 := strconv.Atoi(container)
		if err1 != nil || err2 != nil {
			continue
		}
		out[c] = h
	}
	return out
}

// publishedPorts extracts the actual published host ports from a container
// inspect result, keyed by container port.
func publishedPorts(info *container.InspectResponse) map[int]int {
	out := make(map[int]int)
	if info == nil || info.NetworkSettings == nil {
		return out
	}
	for port, bindings := range info.NetworkSettings.Ports {
		if len(bindings) == 0 {
			continue
		}
		c, err := strconv.Atoi(port.Port())
		if err != nil {
			continue
		}
		h, err := strconv.Atoi(bindings[0].HostPort)
		if err != nil {
			continue
		}
		out[c] = h
	}
	return out
}

// pickHostPort resolves the published host port to probe. When containerPort>0
// the exact mapping is used; otherwise the mapping with the lowest container
// port is chosen deterministically.
func pickHostPort(ports map[int]int, containerPort int) int {
	if containerPort > 0 {
		return ports[containerPort]
	}
	if len(ports) == 0 {
		return 0
	}
	keys := make([]int, 0, len(ports))
	for c := range ports {
		keys = append(keys, c)
	}
	sort.Ints(keys)
	return ports[keys[0]]
}

// shouldRestart reports whether the agent should restart a crashed/unhealthy
// container for the given policy.
func shouldRestart(policy string) bool {
	switch policy {
	case RestartPolicyAlways, RestartPolicyUnlessStopped, RestartPolicyOnFailure:
		return true
	default:
		return false
	}
}

// containerName prefixes managed container names for uniqueness.
// TODO: make the prefix configurable per agent to allow multiple agents on
// one host without name collisions.
func containerName(serviceName string) string {
	return "usenova-" + serviceName
}

// shortID safely returns a shortened container ID for logging.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// snapshot returns a copy of a service by name.
func (m *Manager) snapshot(name string) (*Service, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	svc, ok := m.services[name]
	if !ok {
		return nil, false
	}
	c := *svc
	m.annotateRestartStatusLocked(&c, time.Now())
	return &c, true
}

// annotateRestartStatusLocked fills the status-only restart fields from the
// service's breaker. The caller must hold at least the manager read lock.
func (m *Manager) annotateRestartStatusLocked(s *Service, now time.Time) {
	policy := m.policyForSpecLocked(s.Spec)
	attempts := s.RestartAttempts
	if s.restart != nil {
		policy = s.restart.policy
		attempts = s.restart.countAt(now)
	}

	s.RestartAttempts = attempts
	s.RestartWindowSeconds = int(policy.Window / time.Second)
	if policy.MaxRestarts > 0 {
		remaining := policy.MaxRestarts - attempts
		if remaining < 0 {
			remaining = 0
		}
		s.RestartBudgetRemaining = remaining
	} else {
		s.RestartBudgetRemaining = -1
	}

	if s.restartExhausted {
		// The breaker stays tripped until redeploy, even after the attempts
		// that exhausted it have aged out of the sliding window.
		s.RestartBudgetRemaining = 0
		s.FailedReason = FailedReasonCircuitBreaker
	} else {
		s.FailedReason = ""
	}
}
