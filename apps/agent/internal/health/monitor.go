package health

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ServiceHealth holds health state for a monitored service
type ServiceHealth struct {
	ServiceID         string
	Config            CheckConfig
	ConsecutiveFails  int
	ConsecutivePasses int
	LastCheck         time.Time
	LastResult        CheckResult
	IsHealthy         bool
	RestartCount      int
}

// OnUnhealthyFunc is called when a service becomes unhealthy
type OnUnhealthyFunc func(ctx context.Context, serviceID string) error

// Monitor monitors service health and triggers actions on failure
type Monitor struct {
	checker       *Checker
	services      map[string]*ServiceHealth
	mu            sync.RWMutex
	stopCh        chan struct{}
	stopOnce      sync.Once
	wg            sync.WaitGroup
	log           *logrus.Logger
	onUnhealthy   OnUnhealthyFunc
	restartPolicy string // "always" or "on-failure"
	enabled       bool
	started       bool
	runCtx        context.Context
}

// NewMonitor creates a new health monitor
func NewMonitor(log *logrus.Logger, onUnhealthy OnUnhealthyFunc) *Monitor {
	return &Monitor{
		checker:       NewChecker(log),
		services:      make(map[string]*ServiceHealth),
		stopCh:        make(chan struct{}),
		log:           log,
		onUnhealthy:   onUnhealthy,
		restartPolicy: "on-failure", // default policy
		enabled:       true,
	}
}

// SetRestartPolicy sets the restart policy for unhealthy services
func (m *Monitor) SetRestartPolicy(policy string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restartPolicy = policy
}

// AddService adds a service to be monitored. Safe to call before or after Start.
func (m *Monitor) AddService(serviceID string, config CheckConfig) {
	m.mu.Lock()

	if _, exists := m.services[serviceID]; exists {
		m.log.WithField("service", serviceID).Warn("Service already monitored, updating config")
	}

	entry := &ServiceHealth{
		ServiceID:         serviceID,
		Config:            config,
		ConsecutiveFails:  0,
		ConsecutivePasses: 0,
		IsHealthy:         true, // Assume healthy initially
		RestartCount:      0,
	}
	m.services[serviceID] = entry
	shouldStart := m.started
	ctx := m.runCtx
	m.mu.Unlock()

	if shouldStart {
		m.wg.Add(1)
		go func(h *ServiceHealth) {
			defer m.wg.Done()
			m.monitorService(ctx, serviceID, h)
		}(entry)
	}

	m.log.WithFields(logrus.Fields{
		"service":  serviceID,
		"type":     config.Type,
		"interval": config.Interval,
	}).Info("Added service to health monitor")
}

// RemoveService removes a service from monitoring
func (m *Monitor) RemoveService(serviceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.services, serviceID)
	m.log.WithField("service", serviceID).Info("Removed service from health monitor")
}

// Start starts the health monitoring loop
func (m *Monitor) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.runCtx = ctx
	targets := make(map[string]*ServiceHealth, len(m.services))
	for id, h := range m.services {
		targets[id] = h
	}
	m.mu.Unlock()

	m.log.Info("Starting health monitor")

	for serviceID, health := range targets {
		m.wg.Add(1)
		go func(id string, h *ServiceHealth) {
			defer m.wg.Done()
			m.monitorService(ctx, id, h)
		}(serviceID, health)
	}
}

// Stop stops the health monitor
func (m *Monitor) Stop() {
	m.stopOnce.Do(func() {
		m.log.Info("Stopping health monitor")
		close(m.stopCh)
		m.wg.Wait()
		m.log.Info("Health monitor stopped")
	})
}

// monitorService monitors a single service
func (m *Monitor) monitorService(ctx context.Context, serviceID string, health *ServiceHealth) {
	interval := health.Config.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	m.log.WithFields(logrus.Fields{
		"service":  serviceID,
		"interval": interval,
	}).Debug("Starting service monitor")

	for {
		select {
		case <-ticker.C:
			// Exit if this service was removed or replaced (new pointer).
			m.mu.RLock()
			current, ok := m.services[serviceID]
			removed := !ok || current != health
			enabled := m.enabled
			m.mu.RUnlock()
			if removed {
				m.log.WithField("service", serviceID).Debug("Service no longer monitored, stopping monitor")
				return
			}

			if !enabled {
				continue
			}

			result := m.checker.Check(ctx, health.Config)

			m.mu.Lock()
			health.LastCheck = result.Time
			health.LastResult = result

			if result.Healthy {
				health.ConsecutiveFails = 0
				health.ConsecutivePasses++
				if !health.IsHealthy && health.ConsecutivePasses >= health.Config.SuccessThreshold {
					health.IsHealthy = true
					m.log.WithField("service", serviceID).Info("Service recovered")
				} else if health.IsHealthy {
					// Still healthy
				}
			} else {
				health.ConsecutivePasses = 0
				health.ConsecutiveFails++
				// Mark as unhealthy if threshold reached
				if health.IsHealthy && health.Config.FailureThreshold > 0 {
					if health.ConsecutiveFails >= health.Config.FailureThreshold {
						health.IsHealthy = false
						m.log.WithField("service", serviceID).Warn("Service became unhealthy, triggering restart")
						if m.onUnhealthy != nil {
							go func(sid string) {
								restartCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
								defer cancel()
								if err := m.onUnhealthy(restartCtx, sid); err != nil {
									m.log.WithError(err).Error("Failed to restart unhealthy service")
								} else {
									m.mu.Lock()
									health.RestartCount++
									// Reset consecutive fails after restart attempt
									health.ConsecutiveFails = 0
									m.mu.Unlock()
									m.log.WithField("service", sid).Info("Unhealthy service restarted")
								}
							}(serviceID)
						}
					}
				} else if health.IsHealthy && health.Config.FailureThreshold == 0 {
					// If no threshold specified, restart immediately on any failure
					health.IsHealthy = false
					if m.onUnhealthy != nil {
						go func(sid string) {
							restartCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
							defer cancel()
							if err := m.onUnhealthy(restartCtx, sid); err != nil {
								m.log.WithError(err).Error("Failed to restart unhealthy service")
							} else {
								m.mu.Lock()
								health.RestartCount++
								health.ConsecutiveFails = 0
								m.mu.Unlock()
							}
						}(serviceID)
					}
				}
			}
			m.mu.Unlock()

		case <-m.stopCh:
			m.log.WithField("service", serviceID).Debug("Stopping service monitor")
			return

		case <-ctx.Done():
			m.log.WithField("service", serviceID).Debug("Service monitor stopping due to context")
			return
		}
	}
}

// GetServiceHealth gets health status for a service
func (m *Monitor) GetServiceHealth(serviceID string) (*ServiceHealth, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	health, exists := m.services[serviceID]
	if !exists {
		return nil, false
	}
	// Return a copy
	h := *health
	return &h, true
}

// GetAllHealth gets health status for all services
func (m *Monitor) GetAllHealth() map[string]*ServiceHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*ServiceHealth, len(m.services))
	for id, h := range m.services {
		hCopy := *h
		result[id] = &hCopy
	}
	return result
}

// SetEnabled enables or disables monitoring
func (m *Monitor) SetEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = enabled
}
