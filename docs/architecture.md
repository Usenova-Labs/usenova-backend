# Usenova Architecture

> This document describes the high-level architecture of Usenova

## Overview

Usenova enables users to deploy and manage always-on backend services on their own
infrastructure while facilitating payments through the Stellar blockchain. The system is
designed for simplicity, security, and future extensibility into a compute marketplace.

## Core Principles

1. **User Control**: Users maintain full control over their infrastructure
2. **Simplicity First**: Start with essential features, avoid premature complexity
3. **Blockchain Integration**: Leverage Stellar for transparent, low-cost payments
4. **Security by Default**: Secure communication and isolated execution
5. **Observable**: Rich metrics and logging throughout the system

## System Components

### 1. CLI (Command-Line Interface)

**Purpose**: Primary interface for developers and operators

**Responsibilities**:
- Service deployment and configuration
- Agent installation and management
- Payment configuration
- Service monitoring and log retrieval
- Authentication with control plane

**Technology**: Go (single binary, cross-platform)

**Key Features**:
- Interactive prompts for configuration
- Config file support (YAML/JSON)
- Output formatting (JSON, table, YAML)
- Plugin system for extensibility (future)

### 2. Agent (Infrastructure Daemon)

**Purpose**: Runs on user infrastructure to manage services

**Responsibilities**:
- Service lifecycle management (start, stop, restart)
- Health monitoring and automatic recovery
- Resource usage tracking
- Secure communication with control plane
- Local log collection
- Security policy enforcement

**Technology**: Go (lightweight, efficient daemon)

**Key Features**:
- Minimal resource footprint (<50MB memory)
- Docker integration for service isolation
- Configurable health checks
- Rate limiting and resource quotas
- Self-updating capability (future)

**Deployment**:
- Single binary installation
- Systemd/launchd integration
- Configuration via local file or control plane

### 3. Control Plane (Central Coordinator)

**Purpose**: Central API server for coordination and payments

**Responsibilities**:
- User and agent authentication
- Service registry and discovery
- Payment processing coordination
- Metrics aggregation
- Webhook management
- Audit logging

**Technology**: Go (high-performance HTTP API)

**Key Features**:
- RESTful API
- WebSocket support for real-time updates
- JWT-based authentication
- Rate limiting and DDoS protection
- Multi-region support (future)

**Deployment**:
- Containerized application
- Horizontal scaling behind load balancer
- Database: PostgreSQL
- Cache: Redis

### 4. Smart Contracts (Soroban/Stellar)

**Purpose**: On-chain payment and settlement logic

**Responsibilities**:
- Escrow payment handling
- Service-level agreement (SLA) enforcement
- Automated payment releases
- Dispute resolution (future)
- Reputation tracking (future)

**Technology**: Rust (Soroban smart contracts)

**Key Contracts**:
- **PaymentEscrow**: Holds funds until service delivery confirmed
- **ServiceRegistry**: On-chain service metadata (future)
- **DisputeResolver**: Handles payment disputes (future)

## Data Flow

### Remote Deploy Command Channel Flow

The remote deploy channel lets operators send `deploy`, `stop`, and `remove`
commands to an agent running behind NAT. The agent always polls the CP — no
inbound ports required. See [docs/remote-deploy.md](remote-deploy.md) for the
full design note and diagrams.

```
operator ──enqueue──▶ CP queue ──poll──▶ agent ──execute──▶ runtime manager
                              ▲                                   │
                              └───────────result───────────────────┘
```

### Service Deployment Flow

```
1. User runs: `nova deploy myservice`
2. CLI authenticates with Control Plane
3. CLI packages service config and sends to Control Plane
4. Control Plane validates config and assigns to Agent
5. Agent receives deployment request via WebSocket
6. Agent pulls container image and starts service
7. Agent reports status back to Control Plane
8. Control Plane updates service registry
9. CLI displays deployment status to user
```

### Payment Flow

```
1. Consumer initiates payment for service usage
2. Control Plane creates escrow on Stellar via PaymentEscrow contract
3. Consumer deposits funds to escrow
4. Service runs and provides value
5. Agent reports service completion to Control Plane
6. Control Plane triggers escrow release
7. Smart contract releases funds to Provider
8. Both parties receive payment confirmation
```

### Health Check Flow

```
1. Agent performs periodic health checks (every 30s)
2. If service unhealthy:
   a. Agent attempts restart (up to 3 times)
   b. If restart fails, Agent notifies Control Plane
   c. Control Plane updates service status
   d. Control Plane can trigger alerts/webhooks
3. Agent reports metrics to Control Plane (every 60s)
4. Control Plane aggregates metrics for dashboards
```

### Agent Registration Flow

```
1. Agent starts and detects its machine inventory (best-effort):
   OS, architecture, CPU count, total memory, Docker engine version
2. Agent registers with the Control Plane, reporting the inventory
3. Control Plane stores the inventory with the agent record
4. Every heartbeat refreshes the inventory, so dynamic values (e.g. a Docker
   engine upgrade) stay current without re-registering
5. Control Plane returns the inventory on
   GET /api/v1/agents and GET /api/v1/agents/{agent_id}
```

The inventory is optional on the wire: agents that do not report it keep
registering successfully, and the control plane stores an empty inventory for
them. It is the basis for the capacity limits, scheduling and capacity ads of
the compute marketplace.

## Security Model

### Authentication & Authorization

- **CLI ↔ Control Plane**: JWT tokens, refresh token rotation
- **Agent ↔ Control Plane**: Mutual TLS (mTLS) with certificate rotation
- **User ↔ Smart Contracts**: Stellar wallet signatures

### Network Security

- All communication over TLS 1.3
- Agent-initiated connections (no inbound ports required)
- WebSocket heartbeats to detect connection issues
- IP allowlisting for Control Plane API (optional)

### Data Security

- Secrets encrypted at rest (AES-256)
- Sensitive config encrypted in transit
- No PII stored in smart contracts
- Audit logs for all critical operations

## Scalability Considerations

### Current (MVP)

- Single Control Plane instance
- Support for 100s of agents
- Support for 1000s of services
- PostgreSQL with read replicas

### Future (Marketplace)

- Multi-region Control Plane deployment
- Distributed agent discovery
- Sharded databases
- Event-driven architecture with message queue
- CDN for static assets and container images

## Technology Stack Summary

| Component       | Language | Key Libraries              |
|-----------------|----------|----------------------------|
| CLI             | Go       | cobra, viper               |
| Agent           | Go       | logrus, Docker SDK         |
| Control Plane   | Go       | gin, pgx, redis            |
| Smart Contracts | Rust     | soroban-sdk                |
| Database        | -        | PostgreSQL                 |
| Cache           | -        | Redis                      |
| Blockchain      | -        | Stellar (Soroban)          |

## Future Architecture Extensions

### Compute Marketplace

- Service discovery and browsing
- Pricing and bidding mechanisms
- Reputation and rating system
- Multi-provider support
- Geographic routing

### Advanced Features

- Auto-scaling based on demand
- Blue-green deployments
- Canary releases
- Advanced monitoring with APM integration
- Cost optimization recommendations

## Development Phases

### Phase 1: MVP (Current)
- ✅ Project structure
- 🔄 Basic CLI commands
- 🔄 Agent service management
- 🔄 Control Plane API
- 🔄 Simple payment escrow contract

### Phase 2: Production Ready
- Enhanced monitoring and alerting
- Production deployment guides
- Comprehensive testing
- Security audit
- Documentation completion

### Phase 3: Marketplace Features
- Service discovery
- Dynamic pricing
- Multi-provider support
- Advanced analytics

## Diagrams

### High-Level System Diagram

```
┌─────────┐
│   CLI   │ (User's machine)
└────┬────┘
     │ HTTPS
     ▼
┌─────────────────┐
│ Control Plane   │ (Central)
│  - API Server   │
│  - PostgreSQL   │
│  - Redis        │
└────┬────┬───────┘
     │    │
     │    └─────────────────┐
     │ mTLS                 │ Stellar RPC
     ▼                      ▼
┌──────────┐         ┌─────────────────┐
│  Agent   │         │ Stellar Network │
│ (User's  │         │   - PaymentEscrow│
│  infra)  │         │   - Contracts    │
└────┬─────┘         └──────────────────┘
     │
     ▼
┌─────────────┐
│  Services   │
│ (Containers)│
└─────────────┘
```

---

**Last Updated**: 2026-10-14  
**Status**: MVP Architecture - Subject to change based on learnings
