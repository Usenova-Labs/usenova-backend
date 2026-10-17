package runtime

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/sirupsen/logrus"

	"github.com/byoi-platform/agent/internal/docker"
	"github.com/byoi-platform/agent/internal/health"
)

const (
	defaultReconcileInterval = 60 * time.Second

	// Labels applied to every container we manage so foreign containers can
	// be distinguished from agent-owned ones.
	labelManaged = "usenova.managed"
	labelService = "usenova.service"
)

// ContainerRuntime is the subset of the Docker wrapper the manager depends on.
// It is an interface so the runtime can be unit-tested without a daemon.
type ContainerRuntime interface {
	IsAvailable(ctx context.Context) bool
	PullImage(ctx context.Context, image string, authConfig *types.AuthConfig) error
	RunContainer(ctx context.Context, cfg docker.ContainerConfig) (string, error)
	StartContainer(ctx context.Context, containerID string) error
	StopContainer(ctx context.Context, containerID string, timeout time.Duration) error
	RestartContainer(ctx context.Context, containerID string, timeout time.Duration) error
	RemoveContainer(ctx context.Context, containerID string, force bool) error
	InspectContainer(ctx context.Context, containerID string) (*types.ContainerJSON, error)
	ListContainers(ctx context.Context, all bool, filters filters.Args) ([]types.Container, error)
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
	reconcileStop  chan struct{}
	reconcileDone  sync.WaitGroup
	reconcileEvery time.Duration
}

// NewManager creates a runtime manager backed by the given container runtime.
func NewManager(dc ContainerRuntime, log *logrus.Logger) *Manager {
	m := &Manager{
		docker:         dc,
		log:            log,
		services:       make(map[string]*Service),
		reconcileEvery: defaultReconcileInterval,
	}

	// Health monitor restarts unhealthy services through our own handler.
	m.monitor = health.NewMonitor(log, m.onUnhealthy)
	m.monitor.SetRestartPolicy(RestartPolicyOnFailure)

	return m
}

// Start launches the health monitor and the reconciliation loop.
// It is not an error for the Docker daemon to be unreachable right now:
// reconciliation will pick it up when it comes back. TODO: emit an agent
// status/capability update when Docker availability changes.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return fmt.Errorf("runtime manager already started")
	}
	m.started = true
	m.reconcileStop = make(chan struct{})
	m.mu.Unlock()

	if m.docker == nil || !m.docker.IsAvailable(ctx) {
		m.log.Warn("Docker daemon not available at startup; will keep retrying via reconcile loop")
	}

	m.monitor.Start(ctx)

	m.reconcileDone.Add(1)
	go m.reconcileLoop(ctx)

	m.log.Info("Runtime manager started")
	return nil
}

// Stop shuts down the reconcile loop and health monitors cleanly.
func (m *Manager) Stop(ctx context.Context) {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	m.started = false
	stop := m.reconcileStop
	m.mu.Unlock()

	if stop != nil {
		close(stop)
	}

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
	m.log.Info("Runtime manager stopped")
}

// DeployService pulls the image, creates and starts the container, and
// registers health monitoring. TODO: support rolling redeploy of an already
// running service instead of rejecting it.
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

	m.mu.Lock()
	if existing, ok := m.services[spec.Name]; ok && existing.State == ServiceStateRunning {
		m.mu.Unlock()
		return nil, fmt.Errorf("service %s is already running (stop it first to redeploy)", spec.Name)
	}
	svc := &Service{
		Spec:  spec,
		State: ServiceStatePending,
	}
	m.services[spec.Name] = svc
	m.mu.Unlock()

	if err := m.deploy(ctx, svc); err != nil {
		m.failService(spec.Name, err)
		return nil, err
	}

	deployed, ok := m.snapshot(spec.Name)
	if !ok {
		return nil, fmt.Errorf("service %s disappeared after deploy", spec.Name)
	}
	return deployed, nil
}

// deploy performs the actual pull + create + start sequence.
func (m *Manager) deploy(ctx context.Context, svc *Service) error {
	spec := svc.Spec

	// Pull image.
	// TODO: skip pull when the image exists locally (image pull policy:
	// always / if-not-present / never) and support private registry auth.
	if err := m.docker.PullImage(ctx, spec.Image, nil); err != nil {
		return fmt.Errorf("pull image: %w", err)
	}

	cfg := buildContainerConfig(spec)

	// If a container with this service's name (possibly from a previous run
	// of the agent) still exists, remove it so create does not collide.
	if existingID, err := m.findManagedContainer(ctx, spec.Name); err == nil && existingID != "" {
		m.log.WithFields(logrus.Fields{
			"service": spec.Name,
			"id":      shortID(existingID),
		}).Info("Removing stale container before deploy")
		if err := m.docker.RemoveContainer(ctx, existingID, true); err != nil {
			return fmt.Errorf("remove stale container: %w", err)
		}
	}

	id, err := m.docker.RunContainer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("run container: %w", err)
	}

	hasHealth := false
	m.mu.Lock()
	svc.ContainerID = id
	svc.State = ServiceStateRunning
	svc.DeployedAt = time.Now()
	svc.LastError = nil
	if healthCfg := buildHealthCheck(spec); healthCfg != nil {
		svc.Health = healthCfg
		hasHealth = true
		m.monitor.AddService(spec.Name, *healthCfg)
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
	id := svc.ContainerID
	m.mu.Unlock()

	if id != "" {
		if err := m.docker.StopContainer(ctx, id, 30*time.Second); err != nil {
			return fmt.Errorf("stop service %s: %w", name, err)
		}
	}

	m.monitor.RemoveService(name)

	m.mu.Lock()
	svc.State = ServiceStateStopped
	m.mu.Unlock()

	m.log.WithField("service", name).Info("Service stopped")
	return nil
}

// RemoveService stops and removes the container and forgets the service.
func (m *Manager) RemoveService(ctx context.Context, name string) error {
	m.mu.Lock()
	svc, ok := m.services[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("service %s not found", name)
	}
	id := svc.ContainerID
	delete(m.services, name)
	m.mu.Unlock()

	m.monitor.RemoveService(name)

	if id != "" {
		if err := m.docker.RemoveContainer(ctx, id, true); err != nil {
			return fmt.Errorf("remove service %s container: %w", name, err)
		}
	}

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

// GetService returns a snapshot of a managed service by name.
func (m *Manager) GetService(name string) (*Service, bool) {
	return m.snapshot(name)
}

// ListServices returns snapshots of all managed services.
func (m *Manager) ListServices() []*Service {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Service, 0, len(m.services))
	for _, svc := range m.services {
		c := *svc
		out = append(out, &c)
	}
	return out
}

// Health returns the underlying health monitor (for status reporting).
func (m *Manager) Health() *health.Monitor {
	return m.monitor
}

// onUnhealthy is invoked by the health monitor when a service fails its
// checks. Restarts the container according to the service restart policy.
func (m *Manager) onUnhealthy(ctx context.Context, name string) error {
	m.mu.RLock()
	svc, ok := m.services[name]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("unhealthy callback for unknown service %s", name)
	}
	id := svc.ContainerID
	policy := svc.Spec.RestartPolicy
	state := svc.State
	m.mu.RUnlock()

	if state == ServiceStateStopped {
		m.log.WithField("service", name).Debug("Ignoring unhealthy signal for stopped service")
		return nil
	}

	if !shouldRestart(policy) {
		m.log.WithFields(logrus.Fields{
			"service": name,
			"policy":  policy,
		}).Warn("Service unhealthy but restart policy forbids restart")
		m.mu.Lock()
		svc.State = ServiceStateUnhealthy
		m.mu.Unlock()
		return nil
	}

	if id == "" {
		return fmt.Errorf("service %s has no container to restart", name)
	}

	m.log.WithFields(logrus.Fields{
		"service": name,
		"id":      shortID(id),
		"policy":  policy,
	}).Warn("Restarting unhealthy service")

	if err := m.docker.RestartContainer(ctx, id, 30*time.Second); err != nil {
		return fmt.Errorf("restart service %s: %w", name, err)
	}

	m.mu.Lock()
	svc.RestartCount++
	svc.State = ServiceStateRunning
	svc.LastError = nil
	m.mu.Unlock()

	// TODO: back off / give up after N consecutive restarts (circuit breaker).
	return nil
}

// reconcileLoop periodically inspects managed containers and restarts
// crashed ones according to their restart policy.
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
		name   string
		id     string
		policy string
		state  ServiceState
	}
	items := make([]item, 0, len(m.services))
	for name, svc := range m.services {
		items = append(items, item{name, svc.ContainerID, svc.Spec.RestartPolicy, svc.State})
	}
	m.mu.RUnlock()

	for _, it := range items {
		if it.state == ServiceStateStopped || it.state == ServiceStateFailed {
			// Intentionally not running; leave alone.
			continue
		}
		if it.id == "" {
			continue
		}

		info, err := m.docker.InspectContainer(ctx, it.id)
		if err != nil {
			// Container is gone (removed externally). Recreate if policy wants it running.
			if shouldRestart(it.policy) {
				m.log.WithFields(logrus.Fields{
					"service": it.name,
					"error":   err.Error(),
				}).Warn("Managed container missing; redeploying")
				if err := m.redeploy(ctx, it.name); err != nil {
					m.log.WithError(err).Error("Reconcile redeploy failed")
				}
			} else {
				m.mu.Lock()
				if svc, ok := m.services[it.name]; ok {
					svc.State = ServiceStateFailed
					svc.LastError = err
				}
				m.mu.Unlock()
			}
			continue
		}

		if info.State.Running {
			m.mu.Lock()
			if svc, ok := m.services[it.name]; ok {
				svc.State = ServiceStateRunning
			}
			m.mu.Unlock()
			continue
		}

		// Container exists but is not running.
		restartable := it.policy == RestartPolicyAlways ||
			it.policy == RestartPolicyUnlessStopped ||
			(it.policy == RestartPolicyOnFailure && info.State.ExitCode != 0)

		if restartable {
			m.log.WithFields(logrus.Fields{
				"service":   it.name,
				"exit_code": info.State.ExitCode,
				"policy":    it.policy,
			}).Warn("Managed container exited; restarting")
			if err := m.docker.StartContainer(ctx, it.id); err != nil {
				// Start of an exited container can fail on some setups; fall
				// back to remove + redeploy.
				m.log.WithError(err).Warn("Start failed; redeploying container")
				if err := m.redeploy(ctx, it.name); err != nil {
					m.log.WithError(err).Error("Reconcile redeploy failed")
				}
				continue
			}
			m.mu.Lock()
			if svc, ok := m.services[it.name]; ok {
				svc.RestartCount++
				svc.State = ServiceStateRunning
			}
			m.mu.Unlock()
		} else {
			m.mu.Lock()
			if svc, ok := m.services[it.name]; ok {
				svc.State = ServiceStateFailed
				svc.LastError = fmt.Errorf("container exited with code %d", info.State.ExitCode)
			}
			m.mu.Unlock()
		}
	}
}

// redeploy tears down (if needed) and redeploys a service from its desired spec.
func (m *Manager) redeploy(ctx context.Context, name string) error {
	m.mu.RLock()
	svc, ok := m.services[name]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("service %s not found", name)
	}
	spec := svc.Spec
	oldID := svc.ContainerID
	m.mu.RUnlock()

	if oldID != "" {
		if err := m.docker.RemoveContainer(ctx, oldID, true); err != nil {
			m.log.WithError(err).Debug("Could not remove old container during redeploy (may be gone)")
		}
	}

	deployCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	fresh := &Service{Spec: spec, State: ServiceStatePending}
	if err := m.deploy(deployCtx, fresh); err != nil {
		m.failService(name, err)
		return err
	}

	// deploy() mutates the Service it is given, so point the map at it.
	m.mu.Lock()
	m.services[name] = fresh
	m.mu.Unlock()

	return nil
}

// failService marks a service as failed and stops monitoring it.
func (m *Manager) failService(name string, err error) {
	m.mu.Lock()
	if svc, ok := m.services[name]; ok {
		svc.State = ServiceStateFailed
		svc.LastError = err
	}
	m.mu.Unlock()
	m.monitor.RemoveService(name)
	m.log.WithFields(logrus.Fields{"service": name, "error": err.Error()}).Error("Service deployment failed")
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
	return &c, true
}

// findManagedContainer looks up a container by our service label.
func (m *Manager) findManagedContainer(ctx context.Context, name string) (string, error) {
	containers, err := m.docker.ListContainers(ctx, true, filters.NewArgs())
	if err != nil {
		return "", err
	}
	for _, ctr := range containers {
		if ctr.Labels[labelService] == name {
			return ctr.ID, nil
		}
	}
	return "", nil
}

// buildContainerConfig maps a ServiceSpec to a docker ContainerConfig.
func buildContainerConfig(spec docker.ServiceSpec) docker.ContainerConfig {
	labels := map[string]string{
		labelManaged: "true",
		labelService: spec.Name,
	}
	for k, v := range spec.Labels {
		labels[k] = v
	}

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
		Labels:        labels,
	}
	if spec.Resources != nil {
		cfg.NanoCPUs = spec.Resources.CPULimit
		cfg.Memory = spec.Resources.MemoryLimit
	}
	return cfg
}

// buildHealthCheck derives an agent-side health check from the service spec.
// Returns nil when no health check is configured.
func buildHealthCheck(spec docker.ServiceSpec) *health.CheckConfig {
	hc := spec.HealthCheck
	if hc == nil {
		return nil
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
		hostPort := firstHostPort(spec.Ports, hc.Port)
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
		hostPort := firstHostPort(spec.Ports, hc.Port)
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

// firstHostPort finds a published host port. If containerPort > 0 it matches
// against the container side of the mapping; otherwise the first mapping wins.
func firstHostPort(ports map[string]string, containerPort int) int {
	if len(ports) == 0 {
		return 0
	}
	for host, container := range ports {
		if containerPort > 0 {
			c, err := strconv.Atoi(container)
			if err != nil || c != containerPort {
				continue
			}
		}
		h, err := strconv.Atoi(host)
		if err != nil {
			continue
		}
		return h
	}
	return 0
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
