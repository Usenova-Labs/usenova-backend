# Usenova Agent

The Usenova Agent is a lightweight daemon that runs on user infrastructure to manage services and maintain communication with the Control Plane.

## Features

- **Automatic Registration**: Registers with Control Plane on startup
- **Machine Inventory**: Reports OS, architecture, CPU count, memory and Docker version
- **Heartbeat System**: Periodic status updates to maintain connectivity
- **Docker Integration**: Detects and manages Docker-based services (coming soon)
- **Health Monitoring**: Tracks service health and reports status
- **Graceful Shutdown**: Clean shutdown on SIGINT/SIGTERM

## Configuration

The agent is configured via environment variables:

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `CONTROL_PLANE_URL` | Yes | - | Control Plane API endpoint |
| `AGENT_TOKEN` | Yes | - | Authentication token |
| `AGENT_ID` | No | auto-generated | Unique agent identifier |
| `PLATFORM_BOOTSTRAP_TOKEN` | No | - | Registers with the platform endpoint instead of BYOI; must match the control plane token |
| `PLATFORM_OWNER_ID` | No | - | Optional fleet owner identifier |
| `PLATFORM_LABELS` | No | - | Optional comma-separated `key=value` fleet tags |
| `HEARTBEAT_INTERVAL` | No | `30s` | Heartbeat frequency |
| `COMMAND_POLL_INTERVAL` | No | `10s` | How often the agent asks the Control Plane for pending commands |
| `RESTART_MAX_RESTARTS` | No | `3` | Restarts allowed per window before a service is failed (`0` = unlimited) |
| `RESTART_WINDOW` | No | `10m` | Sliding window over which restart attempts are counted |
| `RESTART_BACKOFF` | No | `30s` | Base delay between restart attempts (doubles with every attempt, capped at 5m) |
| `AGENT_NETWORK_MODE` | No | `bridge` | Default network policy: `bridge`/`default`, `none`, or `dedicated`/`per-service` (host networking is rejected) |
| `AGENT_NETWORK_NAME` | No | - | Attach services to an existing shared Docker network |
| `AGENT_NETWORK_INTERNAL` | No | `false` | Make agent-created dedicated networks internal (no external connectivity) |
| `DEBUG` | No | - | Enable debug logging |

### Restart limits

A service that keeps crashing (or keeps failing its health checks) is not
restarted forever. The agent counts restart attempts inside a sliding window
and waits between them with an exponential backoff. When the budget is
exhausted the service is marked `failed`, health monitoring stops, and the
agent leaves it alone until it is deployed again.

The limits above apply to every service; a deployment can override them with
`max_restarts`, `restart_window_seconds` and `restart_backoff_seconds` in the
service spec (`nova deploy --max-restarts 5 --restart-window 300`).

The base delay doubles with every attempt and is capped at 5m. A base delay
configured above that cap is never shortened: the cap is raised to the
configured base delay instead.

### Resource limits and network isolation

CPU and memory limits are taken from the deployment's `resources` object. All
values are optional; omitted or zero values keep Docker's defaults (unlimited),
so existing deployments behave exactly as before.

```json
{
  "name": "my-api",
  "image": "nginx:latest",
  "resources": {
    "cpu_limit": 1500000000,
    "memory_limit": 536870912,
    "cpu_reserve": 512,
    "memory_reserve": 268435456
  },
  "network_mode": "dedicated"
}
```

| Field | Docker field | Unit |
| --- | --- | --- |
| `cpu_limit` | `HostConfig.NanoCpus` | nano-CPUs (`1e9` = 1 CPU) |
| `memory_limit` | `HostConfig.Memory` | bytes |
| `cpu_reserve` | `HostConfig.CpuShares` | relative CPU weight (Docker default `1024`) |
| `memory_reserve` | `HostConfig.MemoryReservation` | bytes (soft limit) |

The CLI exposes the same fields as flags, for example:

```bash
nova deploy --name my-api --image nginx:latest \
  --cpus 1.5 --memory 512m --memory-reservation 256m --cpu-shares 512 \
  --network-mode dedicated
```

Network behavior is configurable per agent and per service:

| `network_mode` | Behavior |
| --- | --- |
| `bridge` / `default` (default) | Docker's shared bridge; backward compatible |
| `none` | No network stack (`--network none`) |
| `dedicated` / `per-service` | Agent creates `usenova-<service>` and removes it when the service is removed |
| `host` | Rejected; host networking bypasses container isolation |

`network_name` (or the CLI `--network` flag) attaches a container to an
existing user-managed network. Explicit network names take precedence and are
never deleted by the agent. `network_internal: true` makes an agent-created
dedicated network internal; it has no effect on an existing network.

The agent-wide `AGENT_NETWORK_MODE` is used when a service does not set
`network_mode`. Set it to `dedicated` to isolate every deployment by default,
or to `none` on hosts where containers must not have network access.

Verify the applied limits and network with Docker:

```bash
docker inspect --format '{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.CpuShares}} {{.HostConfig.MemoryReservation}}' my-api
docker inspect --format '{{.HostConfig.NetworkMode}}' my-api
docker network inspect usenova-my-api
```

`NanoCpus` is the hard CPU quota, `Memory` the hard memory limit,
`CpuShares` the relative CPU weight and `MemoryReservation` the soft memory
limit. A value of `0` with an empty `NetworkMode` means the deployment did not
configure that constraint.

### Example Configuration

```bash
export CONTROL_PLANE_URL="https://api.usenova.dev"
export AGENT_TOKEN="your-agent-token-here"
export HEARTBEAT_INTERVAL="30s"
```

Or copy `.env.example` to `.env` and configure:

```bash
cp .env.example .env
# Edit .env with your values
```

## Usage

### Development

```bash
# Build the agent
go build -o bin/usenova-agent

# Run with environment variables
CONTROL_PLANE_URL=http://localhost:8080 \
AGENT_TOKEN=dev-token \
./bin/usenova-agent
```

### Production

```bash
# Install the agent binary
sudo cp usenova-agent /usr/local/bin/

# Create configuration
sudo mkdir -p /etc/usenova
sudo cp .env.example /etc/usenova/agent.env

# Edit configuration
sudo vim /etc/usenova/agent.env

# Run the agent
USENOVA_CONFIG=/etc/usenova/agent.env usenova-agent
```

## Registration

On startup the agent registers with the control plane and reports a
best-effort inventory of the machine it runs on:

```json
{
  "agent_id": "optional-id",
  "hostname": "my-host",
  "version": "1.0.0",
  "capabilities": { "docker": true },
  "system": {
    "os": "linux",
    "arch": "amd64",
    "cpu_count": 8,
    "memory_bytes": 17179869184,
    "docker_version": "24.0.7"
  }
}
```

| Field | Description |
| --- | --- |
| `os` | Operating system (`runtime.GOOS`) |
| `arch` | CPU architecture (`runtime.GOARCH`) |
| `cpu_count` | Number of logical CPUs (`runtime.NumCPU()`) |
| `memory_bytes` | Total physical memory of the host, `0` when it cannot be determined |
| `docker_version` | Docker engine version, empty when Docker is unavailable |

Every value is best-effort: the agent still registers when a value cannot be
determined, and the control plane accepts registrations that omit the
inventory entirely (older agents).

The inventory is refreshed on every heartbeat, so dynamic values (e.g. a
Docker engine upgrade) reach the control plane without a re-registration.

## Local Control API

When `API_ADDR` is set the agent exposes a loopback-only HTTP API for local
tooling (the `nova` CLI, manual testing). It refuses to bind to a non-loopback
address and requires a bearer token (`API_TOKEN`) on every request.

| Method + path | Description |
| --- | --- |
| `GET /services` | List managed services |
| `POST /services` | Deploy a service |
| `GET /services/{name}` | Get one service |
| `DELETE /services/{name}` | Remove a service |
| `POST /services/{name}/stop` | Stop a service |
| `GET /services/{name}/logs` | Stream a service's container logs |

The logs endpoint streams `text/plain` (stdout and stderr merged) and accepts:

| Query | Default | Description |
| --- | --- | --- |
| `tail` | `100` | Lines from the end, or `all` |
| `follow` | `false` | Keep streaming new lines until the client disconnects |
| `timestamps` | `false` | Prefix each line with its RFC3339Nano timestamp |

Errors use the shared `{"error": <code>, "message": <text>}` shape: `404` when
the service is unknown (or has no container yet) and `503` when Docker is
unavailable.

```bash
curl -N -H "Authorization: Bearer $API_TOKEN" \
  "http://127.0.0.1:9090/services/web/logs?tail=100&follow=true"
```

## Control Plane Command Channel

The agent polls the Control Plane for commands, executes them against the
runtime manager and reports the outcome. The Control Plane never connects to
the agent, so the agent keeps working behind NAT and firewalls and the local
control API above stays loopback-only.

| Command | Payload | Effect |
| --- | --- | --- |
| `deploy` | service spec (`name`, `image`, …) | Deploy a service |
| `stop` | `{"name": "…"}` | Stop a running service |
| `remove` | `{"name": "…"}` | Remove a service |

The agent authenticates with the session token the Control Plane issues at
registration. The design, the threat model and the planned evolution of the
channel are documented in [docs/remote-deploy.md](../../docs/remote-deploy.md).

## Architecture

```
┌─────────────────┐       ┌─────────────────┐
│  Usenova Agent  │──────▶│ Control Plane   │
│                 │       │                 │
│ ┌─────────────┐ │       │ ┌─────────────┐ │
│ │ Config      │ │       │ │ Agent API   │ │
│ │ Manager     │ │       │ │             │ │
│ └─────────────┘ │       │ └─────────────┘ │
│                 │       │                 │
│ ┌─────────────┐ │       │ ┌─────────────┐ │
│ │ Control     │◀┼───────┼─│ Service     │ │
│ │ Plane       │ │       │ │ Registry    │ │
│ │ Client      │ │       │ │             │ │
│ └─────────────┘ │       │ └─────────────┘ │
│                 │       └─────────────────┘
│ ┌─────────────┐ │       
│ │ Service     │ │       ┌─────────────────┐
│ │ Manager     │ │──────▶│ Docker/Services │
│ │ (TODO)      │ │       │                 │
│ └─────────────┘ │       └─────────────────┘
└─────────────────┘
```

## Development

### Project Structure

```
apps/agent/
├── main.go                 # Entry point
├── internal/
│   ├── config/            # Configuration management
│   │   └── config.go
│   ├── agent/             # Main orchestration logic
│   │   └── agent.go
│   ├── controlplane/      # Control Plane communication
│   │   ├── client.go      # HTTP client
│   │   └── types.go       # Request/response types
│   └── systeminfo/        # Machine inventory detection
│       └── systeminfo.go
├── .env.example           # Example configuration
└── README.md              # This file
```

### Adding New Features

1. **Service Management**: Extend `internal/agent/` to manage Docker containers
2. **Health Checks**: Add health monitoring capabilities
3. **Metrics**: Implement system and service metrics collection
4. **Configuration**: Add config file support alongside environment variables

### Testing

```bash
# Run tests
go test ./...

# Test with local control plane
export CONTROL_PLANE_URL=http://localhost:8080
export AGENT_TOKEN=test-token
go run main.go
```

## Contributing

See [CONTRIBUTING.md](../../CONTRIBUTING.md) for development guidelines and project conventions.