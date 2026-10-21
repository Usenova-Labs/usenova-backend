# Usenova Agent

The Usenova Agent is a lightweight daemon that runs on user infrastructure to manage services and maintain communication with the Control Plane.

## Features

- **Automatic Registration**: Registers with Control Plane on startup
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
| `HEARTBEAT_INTERVAL` | No | `30s` | Heartbeat frequency |
| `RESTART_MAX_RESTARTS` | No | `3` | Restarts allowed per window before a service is failed (`0` = unlimited) |
| `RESTART_WINDOW` | No | `10m` | Sliding window over which restart attempts are counted |
| `RESTART_BACKOFF` | No | `30s` | Base delay between restart attempts (doubles with every attempt) |
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
│   └── controlplane/      # Control Plane communication
│       ├── client.go      # HTTP client
│       └── types.go       # Request/response types
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