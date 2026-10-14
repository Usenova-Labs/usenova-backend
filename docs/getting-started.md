# Getting Started with BYOI Platform

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
# curl -sSL https://get.byoi.dev | sh
```

### Install the Agent

```bash
# Coming soon: Agent installation
# byoi agent install
```

## Quick Start

### 1. Initialize Your Project

```bash
# Coming soon
byoi init my-service
cd my-service
```

### 2. Configure Your Service

Create a `byoi.yml` configuration file:

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
byoi deploy
```

### 4. Monitor

```bash
# Coming soon
byoi status
byoi logs my-service
```

## Next Steps

- [Architecture Overview](./architecture.md)
- [CLI Reference](./cli-reference.md) (coming soon)
- [Payment Configuration](./payments.md) (coming soon)
- [Troubleshooting](./troubleshooting.md) (coming soon)

## Need Help?

- [GitHub Issues](https://github.com/yourusername/byoi-platform/issues)
- [Documentation](./README.md)
- [Contributing Guide](../CONTRIBUTING.md)

---

**Note**: This project is in early MVP stage. Installation and deployment workflows will be finalized as development progresses.
