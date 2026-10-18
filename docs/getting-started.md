# Getting Started with Usenova

> Quick start guide for deploying your first always-on service

**Status**: This guide is under development as the MVP progresses.

## Prerequisites

- Linux or macOS machine with Docker installed
- Basic familiarity with command-line interfaces
- (Optional) Stellar wallet for payment features

## Installation

### Install the CLI

```bash
# Coming soon: Installation instructions
# curl -sSL https://get.usenova.dev | sh
```

### Install the Agent

```bash
# Coming soon: Agent installation
# nova agent install
```

## Quick Start

### 1. Initialize Your Project

```bash
# Coming soon
nova init my-service
cd my-service
```

### 2. Configure Your Service

Create a `nova.yml` configuration file:

```yaml
# Example configuration (format subject to change)
name: my-service
image: nginx:latest
port: 80
env:
  ENV: production
replicas: 1
```

### 3. Deploy

```bash
# Coming soon
nova deploy
```

### 4. Monitor

```bash
# Coming soon
nova status
nova logs my-service
```

## Next Steps

- [Architecture Overview](./architecture.md)
- [CLI Quick Start](../apps/cli/README.md)
- [Payment Configuration](./payments.md) (coming soon)
- [Troubleshooting](./troubleshooting.md) (coming soon)

## Need Help?

- [GitHub Issues](https://github.com/Usenova-Labs/usenova-backend/issues)
- [Documentation](./README.md)
- [Contributing Guide](../CONTRIBUTING.md)

---

**Note**: This project is in early MVP stage. Installation and deployment workflows will be finalized as development progresses.
