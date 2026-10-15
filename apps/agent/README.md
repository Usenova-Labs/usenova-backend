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
| `DEBUG` | No | - | Enable debug logging |

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