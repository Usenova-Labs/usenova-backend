package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/sirupsen/logrus"
)

// Client wraps the Docker SDK client
type Client struct {
	cli *client.Client
	log *logrus.Logger
}

// NewClient creates a new Docker client
func NewClient(log *logrus.Logger) (*Client, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create docker client: %w", err)
	}

	return &Client{
		cli: cli,
		log: log,
	}, nil
}

// IsAvailable checks if Docker daemon is available
func (c *Client) IsAvailable(ctx context.Context) bool {
	if c == nil || c.cli == nil {
		return false
	}
	_, err := c.cli.Ping(ctx)
	if err != nil {
		c.log.WithError(err).Debug("Docker ping failed")
		return false
	}
	return true
}

// ServerVersion returns the version of the Docker engine the client is
// connected to.
func (c *Client) ServerVersion(ctx context.Context) (string, error) {
	if c == nil || c.cli == nil {
		return "", errors.New("docker client is not initialised")
	}

	version, err := c.cli.ServerVersion(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get docker version: %w", err)
	}

	return version.Version, nil
}

// Close closes the Docker client connection
func (c *Client) Close() error {
	if c.cli != nil {
		return c.cli.Close()
	}
	return nil
}

// PullImage pulls an image from a registry.
//
// ImagePull reports per-layer failures as JSON records inside the returned
// stream, not necessarily through the method's error return. We must decode the
// stream and surface those errors, otherwise a failed pull can look successful
// and run a stale local image instead. Registry credentials are never logged.
func (c *Client) PullImage(ctx context.Context, image string, authConfig *types.AuthConfig) error {
	// Only the image reference is logged: authConfig carries credentials.
	c.log.WithField("image", image).Info("Pulling image")

	var opts types.ImagePullOptions
	if authConfig != nil {
		encodedAuth, err := encodeAuthConfig(authConfig)
		if err != nil {
			return fmt.Errorf("failed to encode registry credentials for %s: %w", image, err)
		}
		opts.RegistryAuth = encodedAuth
	}

	reader, err := c.cli.ImagePull(ctx, image, opts)
	if err != nil {
		return fmt.Errorf("failed to pull image %s: %w", image, err)
	}
	defer reader.Close()

	// Decode the pull stream so in-band errors are not swallowed.
	dec := json.NewDecoder(reader)
	for {
		var msg jsonmessage.JSONMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("error reading pull output for %s: %w", image, err)
		}
		if msg.Error != nil {
			return fmt.Errorf("error pulling image %s: %s", image, msg.Error.Message)
		}
		// Progress/status messages are intentionally not logged verbosely to
		// avoid emitting auxiliary registry details.
	}

	c.log.WithField("image", image).Info("Image pulled successfully")
	return nil
}

// CreateContainer creates a new container
func (c *Client) CreateContainer(ctx context.Context, cfg ContainerConfig) (string, error) {
	containerConfig, hostConfig, _ := buildContainerConfig(cfg)

	resp, err := c.cli.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, cfg.Name)
	if err != nil {
		return "", fmt.Errorf("failed to create container %s: %w", cfg.Name, err)
	}

	c.log.WithFields(logrus.Fields{
		"name": cfg.Name,
		"id":   shortID(resp.ID),
	}).Info("Container created")
	return resp.ID, nil
}

// StartContainer starts a container
func (c *Client) StartContainer(ctx context.Context, containerID string) error {
	err := c.cli.ContainerStart(ctx, containerID, types.ContainerStartOptions{})
	if err != nil {
		return fmt.Errorf("failed to start container %s: %w", containerID, err)
	}

	c.log.WithField("id", shortID(containerID)).Info("Container started")
	return nil
}

// StopContainer stops a container
func (c *Client) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	stopTimeout := int(timeout.Seconds())
	err := c.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &stopTimeout})
	if err != nil {
		// Idempotent: stopping a container that is already gone is success.
		if errdefs.IsNotFound(err) {
			c.log.WithField("id", shortID(containerID)).Debug("Container already gone; nothing to stop")
			return nil
		}
		return fmt.Errorf("failed to stop container %s: %w", containerID, err)
	}

	c.log.WithField("id", shortID(containerID)).Info("Container stopped")
	return nil
}

// RestartContainer restarts a container
func (c *Client) RestartContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	stopTimeout := int(timeout.Seconds())
	err := c.cli.ContainerRestart(ctx, containerID, container.StopOptions{Timeout: &stopTimeout})
	if err != nil {
		return fmt.Errorf("failed to restart container %s: %w", containerID, err)
	}

	c.log.WithField("id", shortID(containerID)).Info("Container restarted")
	return nil
}

// RemoveContainer removes a container
func (c *Client) RemoveContainer(ctx context.Context, containerID string, force bool) error {
	err := c.cli.ContainerRemove(ctx, containerID, types.ContainerRemoveOptions{
		Force:         force,
		RemoveVolumes: false,
	})
	if err != nil {
		// Idempotent: removing a container that is already gone is success.
		if errdefs.IsNotFound(err) {
			c.log.WithField("id", shortID(containerID)).Debug("Container already gone; nothing to remove")
			return nil
		}
		return fmt.Errorf("failed to remove container %s: %w", containerID, err)
	}

	c.log.WithField("id", shortID(containerID)).Info("Container removed")
	return nil
}

// InspectContainer inspects a container
func (c *Client) InspectContainer(ctx context.Context, containerID string) (*types.ContainerJSON, error) {
	inspect, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container %s: %w", containerID, err)
	}
	return &inspect, nil
}

// GetContainerLogs returns a stream of the container's stdout and stderr.
//
// The reader is already demultiplexed: Docker prefixes every frame with an
// 8-byte header unless the container runs with a TTY, so raw passthrough would
// leak those bytes (and interleave stdout/stderr incorrectly) to callers.
// stdout and stderr are merged into a single plain-text stream.
func (c *Client) GetContainerLogs(ctx context.Context, containerID string, opts LogOptions) (io.ReadCloser, error) {
	info, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect container %s: %w", containerID, err)
	}

	logOptions := types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     opts.Follow,
		Timestamps: opts.Timestamps,
		Tail:       opts.Tail,
	}

	if !opts.Since.IsZero() {
		logOptions.Since = opts.Since.Format(time.RFC3339)
	}

	stream, err := c.cli.ContainerLogs(ctx, containerID, logOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to get logs for container %s: %w", containerID, err)
	}

	if info.Config != nil && info.Config.Tty {
		// TTY containers emit an unframed byte stream; nothing to demultiplex.
		return stream, nil
	}
	return demuxLogStream(stream), nil
}

// demuxLogStream adapts Docker's multiplexed log stream to a plain reader.
// Both output streams are written to the same destination, so callers see one
// ordered stream of text lines. The underlying stream is closed when
// demultiplexing finishes, and a reader closed early aborts the pump.
func demuxLogStream(src io.ReadCloser) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		_, err := stdcopy.StdCopy(pw, pw, src)
		_ = src.Close()
		// CloseWithError(io.EOF) is indistinguishable from a clean Close for
		// readers, so a nil error still ends the stream normally.
		_ = pw.CloseWithError(err)
	}()
	return pr
}

// ListContainers lists containers with filters
func (c *Client) ListContainers(ctx context.Context, all bool, filters filters.Args) ([]types.Container, error) {
	return c.cli.ContainerList(ctx, types.ContainerListOptions{
		All:     all,
		Filters: filters,
	})
}

// GetContainerStats gets container statistics (basic)
func (c *Client) GetContainerStats(ctx context.Context, containerID string) (*types.StatsJSON, error) {
	stats, err := c.cli.ContainerStats(ctx, containerID, false)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats for %s: %w", containerID, err)
	}
	defer stats.Body.Close()

	var statsJSON types.StatsJSON
	if err := json.NewDecoder(stats.Body).Decode(&statsJSON); err != nil {
		return nil, fmt.Errorf("failed to decode stats: %w", err)
	}
	return &statsJSON, nil
}

// WaitForContainer waits for container to exit
func (c *Client) WaitForContainer(ctx context.Context, containerID string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error) {
	return c.cli.ContainerWait(ctx, containerID, condition)
}

// RunContainer creates and starts a container in one operation
func (c *Client) RunContainer(ctx context.Context, cfg ContainerConfig) (string, error) {
	id, err := c.CreateContainer(ctx, cfg)
	if err != nil {
		return "", err
	}

	if err := c.StartContainer(ctx, id); err != nil {
		// Try to clean up on failure
		if rmErr := c.RemoveContainer(ctx, id, true); rmErr != nil {
			c.log.WithError(rmErr).Warn("Failed to cleanup container after start failure")
		}
		return "", err
	}

	return id, nil
}

// encodeAuthConfig encodes auth config for registry
func encodeAuthConfig(authConfig *types.AuthConfig) (string, error) {
	if authConfig == nil {
		return "", nil
	}
	encoded, err := json.Marshal(authConfig)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(encoded), nil
}

// shortID returns a shortened container ID
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
