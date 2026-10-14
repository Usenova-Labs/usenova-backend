# Usenova

> Deploy and manage always-on backends on your own infrastructure with Stellar payments

## Overview

Usenova is an open-source system for deploying, managing, and monetizing always-on backend services on user-controlled infrastructure. The platform integrates with the Stellar network for seamless payments and is designed to evolve into a decentralized compute marketplace.

**Current Status:** MVP - focused on core infrastructure management and Stellar integration

## Key Features

- **Bring Your Own Infrastructure**: Deploy on any infrastructure you control (cloud VMs, bare metal, home servers) for flagship proof of concept, later move to always on free VMs.
- **Always-On Management**: Intelligent agent ensures your services stay running 24/7
- **CLI-First Experience**: Simple, powerful command-line interface for all operations
- **Stellar Integration**: Built-in payment rails using the Stellar blockchain
- **Lightweight Agent**: Minimal resource footprint for the management agent
- **Open Source**: Apache 2.0 licensed, transparent, and extensible

## Architecture

The platform consists of three main components:

### CLI (`apps/cli`)
Command-line interface for developers and operators to:
- Deploy services to their infrastructure
- Monitor service health and logs
- Configure payment settings
- Manage agent lifecycle

### Agent (`apps/agent`)
Lightweight daemon running on user infrastructure that:
- Manages service lifecycle (start, stop, restart, health checks)
- Reports metrics and status to the control plane
- Handles local resource management
- Enforces security policies

### Control Plane (`apps/control-plane`)
Central coordination service that:
- Authenticates users and agents
- Facilitates service discovery
- Manages Stellar payment flows
- Provides API for CLI and monitoring

### Smart Contracts (`contracts`)
Soroban smart contracts on Stellar that handle:
- Escrow and payment settlement
- Service-level agreements (SLAs)
- Reputation and dispute resolution (future)

For detailed architecture documentation, see [docs/architecture.md](docs/architecture.md).

## Tech Stack

- **CLI**: Go - fast, single-binary distribution
- **Agent**: Go - efficient, cross-platform daemon
- **Control Plane**: Go - high performance API server
- **Smart Contracts**: Rust (Soroban) - secure, auditable on-chain logic
- **Blockchain**: Stellar - fast, low-cost payment settlement
- **Shared Packages**: Go libraries for common functionality

## Quick Start

> **Note**: The platform is in early MVP stage. Installation and usage instructions will be added as components stabilize.

```bash
# Coming soon
# Installation, configuration, and deployment examples will be added here
```

## Development

This is a monorepo managed with Go workspaces and Cargo workspaces. See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## Project Structure

```
.
├── apps/
│   ├── cli/              # Command-line interface
│   ├── agent/            # Infrastructure agent daemon
│   └── control-plane/    # Central API server
├── packages/
│   ├── shared/           # Shared Go libraries
│   └── stellar/          # Stellar/Soroban integration
├── contracts/            # Soroban smart contracts
├── docs/                 # Documentation
└── scripts/              # Build and deployment scripts
```

## Contributing

We welcome contributions! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details on:
- Setting up your development environment
- Code style and conventions
- Submitting pull requests
- Reporting issues

## Roadmap

- [x] Project structure and foundation
- [ ] MVP: CLI with basic deployment commands
- [ ] MVP: Agent with service lifecycle management
- [ ] MVP: Control plane API (auth, registration)
- [ ] MVP: Basic Stellar payment integration
- [ ] Future: Compute marketplace features
- [ ] Future: Advanced monitoring and analytics
- [ ] Future: Multi-region orchestration

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Community

- **Issues**: [GitHub Issues](https://github.com/yourusername/byoi-platform/issues)
- **Discussions**: [GitHub Discussions](https://github.com/yourusername/byoi-platform/discussions)
- **Contributing**: See [CONTRIBUTING.md](CONTRIBUTING.md)

---

Built with ❤️ for developers who have felt the pain of cold start and value control over their infrastructure
