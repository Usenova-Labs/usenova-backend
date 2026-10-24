# Usenova

> Always-on backend deploy on your own infrastructure — Nova CLI, agent, control plane, Stellar payments & Soroban escrow

## Overview

Usenova is an open-source system for deploying, managing, and monetizing always-on backend
services on user-controlled infrastructure. The platform integrates with the Stellar
network for payments and uses Soroban smart contracts for escrow. It is designed to evolve
into a decentralized compute marketplace.

**Current Status:** Usenova MVP shipped — agent (service lifecycle, Docker, health checks,
circuit breaker), remote deploy command channel, logs, SQLite agent registry, session auth;
Stellar testnet integration (account linking, balance queries) and Soroban payment-escrow
contract; Host-on-Usenova (managed fleet) is planned.

## Key Features

- **Bring Your Own Infrastructure**: Deploy on any infrastructure you control — cloud VMs,
  bare metal, home servers.
- **Always-On Management**: The agent keeps your services running 24/7 with health checks,
  automatic restarts, and a circuit breaker for crash-looping services.
- **Remote Deploy**: Operators enqueue deploy/stop/remove commands through the control plane;
  the agent polls behind NAT and firewalls. See [docs/remote-deploy.md](docs/remote-deploy.md).
- **CLI-First Experience**: The `nova` CLI handles deploy, status, logs, stop, and remove.
- **Stellar Integration**: Link testnet accounts, query balances, and settle payments via a
  Soroban payment-escrow contract. See [docs/stellar-testnet.md](docs/stellar-testnet.md) and
  [contracts/payment-escrow/](contracts/payment-escrow/).
- **Open Source**: Apache 2.0 licensed, transparent, and extensible.

## Architecture

The platform consists of four components:

### CLI (`apps/cli`)
Command-line interface (`nova`) for developers and operators to:
- Deploy services to their infrastructure
- Monitor service health and logs
- Link Stellar accounts and query balances

See the [CLI quick start](apps/cli/README.md) for build and usage examples.

### Agent (`apps/agent`)
Lightweight daemon running on user infrastructure that:
- Manages service lifecycle (start, stop, restart, health checks, circuit breaker)
- Reports machine inventory and status to the control plane
- Polls the control plane for remote deploy commands
- Handles local resource management via Docker

### Control Plane (`apps/control-plane`)
Central coordination service that:
- Authenticates agents with session tokens
- Maintains a SQLite agent registry
- Manages the remote deploy command channel
- Proxies Stellar account linking and balance queries

### Smart Contracts (`contracts`)
Soroban smart contracts on Stellar that handle:
- Payment escrow (create, release, refund)
- Service-level agreements and dispute resolution (future)

See [docs/remote-deploy.md](docs/remote-deploy.md) for the command channel design and
[docs/architecture.md](docs/architecture.md) for the full architecture overview.

## Tech Stack

- **CLI / Agent / Control Plane**: Go — fast, single-binary distribution
- **Smart Contracts**: Rust (Soroban) — secure, auditable on-chain logic
- **Blockchain**: Stellar — fast, low-cost payment settlement
- **Shared Packages**: Go libraries for Stellar client and common types

## Quick Start

> **What works today:** CLI, agent, control plane, remote command channel, and Stellar
> testnet integration (account linking, balance queries, Soroban escrow contract).
> Packaged releases and the hosted fleet (Host-on-Usenova) are still forthcoming.

```bash
# Build the CLI from source (bin/ is gitignored; subshell keeps you at the repo root)
mkdir -p bin
(cd apps/cli && go build -o ../../bin/nova)

# Point it at a local agent and deploy
export NOVA_API_TOKEN="<your-agent-token>"   # USENOVA_API_TOKEN also works
bin/nova deploy --name web --image nginx:alpine --port 8080:80
bin/nova status
```

See [apps/cli/README.md](apps/cli/README.md) for the full CLI quick start.

To run the whole local path (control plane + agent + CLI + deploy + kill/restart) end to end:

```bash
./scripts/e2e-smoke.sh
```

The script prints a pass/fail line per check and lists the common failures. The same path is
documented step by step in [docs/getting-started.md](docs/getting-started.md).

### Stellar testnet

Link a Stellar testnet account and query its balance through the control plane:

```bash
nova stellar link GBRP... --label payouts
nova stellar balance GBRP...
```

See [docs/stellar-testnet.md](docs/stellar-testnet.md) for testnet setup and
[contracts/payment-escrow/](contracts/payment-escrow/) for the Soroban escrow contract.

## Development

This is a monorepo managed with Go workspaces and Cargo workspaces. See
[CONTRIBUTING.md](CONTRIBUTING.md) for development setup and guidelines.

## Project Structure

```
.
├── apps/
│   ├── cli/              # Command-line interface (nova)
│   ├── agent/            # Infrastructure agent daemon
│   └── control-plane/    # Central API server
├── packages/
│   ├── shared/           # Shared Go libraries
│   └── stellar/          # Stellar/Soroban Go client
├── contracts/            # Soroban smart contracts (payment-escrow)
├── docs/                 # Documentation
└── scripts/              # Build, lint, test and end-to-end smoke scripts
```

## Contributing

We welcome contributions! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details on:
- Setting up your development environment
- Code style and conventions
- Submitting pull requests
- Reporting issues

## Roadmap

- [x] CLI with deployment commands
- [x] Agent with service lifecycle, Docker, health checks, circuit breaker
- [x] Control plane API with agent registration, session auth, SQLite registry
- [x] Remote deploy command channel (poll-based, NAT-friendly)
- [x] Stellar testnet account linking and balance queries
- [x] Soroban payment-escrow contract (create, release, refund)
- [ ] Host-on-Usenova (managed fleet / scheduler) — see [docs/host-on-usenova.md](docs/host-on-usenova.md)
- [ ] Mainnet Stellar support
- [ ] Compute marketplace features

## License

This project is licensed under the Apache License 2.0 — see the [LICENSE](LICENSE) file for
details.

## Community

- **Issues**: [GitHub Issues](https://github.com/Usenova-Labs/usenova-backend/issues)
- **Discussions**: [GitHub Discussions](https://github.com/Usenova-Labs/usenova-backend/discussions)
- **Contributing**: See [CONTRIBUTING.md](CONTRIBUTING.md)

---

Built with ❤️ for developers who have felt the pain of cold start and value control over
their infrastructure.
