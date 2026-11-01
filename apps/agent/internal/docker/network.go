package docker

import (
	"context"
	"fmt"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/sirupsen/logrus"
)

// EnsureNetwork creates a Docker network if it does not exist yet. It is
// idempotent: an existing network with the same name is reused, so redeploys
// and concurrently-starting services do not fail on a duplicate-create race.
func (c *Client) EnsureNetwork(ctx context.Context, cfg NetworkConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("network name is required")
	}

	if existing, err := c.cli.NetworkInspect(ctx, cfg.Name, client.NetworkInspectOptions{}); err == nil {
		// Reuse the existing network only when its Internal setting matches the
		// request — but apply the check only to agent-managed networks.
		// User-managed networks (no ownership label) are always accepted as-is.
		if existing.Network.Labels["usenova.network"] == "true" && existing.Network.Internal != cfg.Internal {
			return fmt.Errorf("network %s already exists with internal=%v, want internal=%v; remove it first",
				cfg.Name, existing.Network.Internal, cfg.Internal)
		}
		return nil
	} else if !errdefs.IsNotFound(err) {
		return fmt.Errorf("inspect network %s: %w", cfg.Name, err)
	}

	driver := cfg.Driver
	if driver == "" {
		driver = "bridge"
	}

	_, err := c.cli.NetworkCreate(ctx, cfg.Name, client.NetworkCreateOptions{
		Driver:   driver,
		Internal: cfg.Internal,
		Labels:   cfg.Labels,
	})
	if err != nil {
		// Another deploy may have created the network between our inspect and
		// create calls. Treat that as success, not as a deploy failure.
		if _, ierr := c.cli.NetworkInspect(ctx, cfg.Name, client.NetworkInspectOptions{}); ierr == nil {
			return nil
		}
		return fmt.Errorf("create network %s: %w", cfg.Name, err)
	}

	c.log.WithFields(logrus.Fields{
		"network":  cfg.Name,
		"driver":   driver,
		"internal": cfg.Internal,
	}).Info("Docker network created")
	return nil
}

// RemoveNetwork removes a Docker network. A network that is already gone is
// treated as success so cleanup is idempotent. Only networks that carry the
// agent ownership label (usenova.network=true) are removed; a network without
// that label is treated as user-managed and left untouched.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}

	info, err := c.cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if err != nil {
		if errdefs.IsNotFound(err) {
			c.log.WithField("network", name).Debug("Docker network already gone; nothing to remove")
			return nil
		}
		return fmt.Errorf("inspect network %s: %w", name, err)
	}

	// Only remove networks the agent created (identified by the ownership label).
	if info.Network.Labels["usenova.network"] != "true" {
		c.log.WithField("network", name).Debug("Docker network is not agent-managed; skipping removal")
		return nil
	}

	if _, err := c.cli.NetworkRemove(ctx, name, client.NetworkRemoveOptions{}); err != nil {
		if errdefs.IsNotFound(err) {
			c.log.WithField("network", name).Debug("Docker network already gone; nothing to remove")
			return nil
		}
		return fmt.Errorf("remove network %s: %w", name, err)
	}
	c.log.WithField("network", name).Info("Docker network removed")
	return nil
}
