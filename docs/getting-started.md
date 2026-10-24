# Getting Started with Usenova

> Quick start guide for deploying your first always-on service

**Status**: this guide covers the local path that works today: control plane, agent and the
`nova` CLI running on one machine. Remote agents and hosted payments are still on the roadmap.

## What this guide covers

| Step | What it proves |
|---|---|
| [1. Build](#1-build) | The three binaries compile (`nova`, `usenova-agent`, `nova-control`) |
| [2. Start the control plane](#2-start-the-control-plane) | The control plane is up and answers `/health` |
| [3. Start the agent](#3-start-the-agent) | The agent registers with the control plane and exposes its local API |
| [4. Deploy a service](#4-deploy-a-service) | `nova deploy` creates a container and publishes a port |
| [5. Check the published port](#5-check-the-published-port) | The service answers on the host port |
| [6. Kill and restart](#6-kill-and-restart) | The service comes back after its container is killed |
| [7. Logs, stop and remove](#7-logs-stop-and-remove) | The remaining CLI commands work end to end |

If you just want the machine-checked version of all of the above, run
[`scripts/e2e-smoke.sh`](../scripts/e2e-smoke.sh):

```bash
./scripts/e2e-smoke.sh
```

It runs every step below, prints a pass/fail line per check, and lists the common failures for
the checks that did not pass.

## Prerequisites

- Linux or macOS with Docker installed and running (`docker info` must succeed)
- Go 1.21+ to build the binaries
- `curl` (used by the smoke script)
- Ports `8080` (control plane), `9090` (agent API) and the published service port free

## 1. Build

```bash
mkdir -p bin
(cd apps/cli && go build -o ../../bin/nova)
(cd apps/agent && go build -o ../../bin/usenova-agent)
(cd apps/control-plane && go build -o ../../bin/nova-control)
```

`scripts/build.sh` does the same for all three binaries at once.

## 2. Start the control plane

```bash
./bin/nova-control
```

The control plane listens on `:8080` and needs no configuration. Check it:

```bash
curl -s http://127.0.0.1:8080/health
# {"message":"Control Plane API is running","status":"healthy"}
```

Registered agents show up on `GET /api/v1/agents`:

```bash
curl -s http://127.0.0.1:8080/api/v1/agents
```

## 3. Start the agent

The agent needs the control plane URL, a token and — for the `nova` CLI — a loopback API address
and token:

```bash
CONTROL_PLANE_URL=http://127.0.0.1:8080 \
AGENT_TOKEN=dev-token \
API_ADDR=127.0.0.1:9090 \
API_TOKEN=dev-api-token \
HEARTBEAT_INTERVAL=30s \
./bin/usenova-agent
```

| Variable | Required | Meaning |
|---|---|---|
| `CONTROL_PLANE_URL` | yes | Where the agent registers and sends heartbeats |
| `AGENT_TOKEN` | yes | Bearer token sent to the control plane |
| `API_ADDR` | no | Enables the loopback-only API the `nova` CLI talks to (`127.0.0.1:9090`) |
| `API_TOKEN` | with `API_ADDR` | Bearer token the CLI must send (`--token` / `NOVA_API_TOKEN` / `USENOVA_API_TOKEN`) |
| `AGENT_ID` | no | Stable agent id, generated when unset |
| `HEARTBEAT_INTERVAL` | no | Heartbeat period, `30s` by default |

Registration is fatal: the agent retries three times (5s apart) and exits when the control plane is
unreachable.

Point the CLI at the agent:

```bash
export NOVA_API_TOKEN=dev-api-token
./bin/nova agent status
# Agent API: http://127.0.0.1:9090 (ok)
# Running services: 0
```

Without `NOVA_API_TOKEN` (or `USENOVA_API_TOKEN` / `--token`) the CLI prints a warning and every
request is rejected with `401 unauthorized`.

## 4. Deploy a service

```bash
./bin/nova deploy \
  --name web \
  --image nginx:alpine \
  --port 18080:80 \
  --health-port 80 \
  --health-path / \
  --restart on-failure
```

The port mapping is **host:container** — `18080:80` publishes the container's port 80 on the host
port 18080. The agent always pulls the image before it creates the container.

`--health-port` takes the **container** port (80), not the host port. With `--port 18080:80` the
health check must point at `80`; `--health-port 18080` resolves to nothing and no health check is
registered. `--health-port` also switches the check to HTTP, so it can be combined with
`--health-path`.

The deployed service shows up on the agent:

```bash
./bin/nova status
# NAME   STATE    IMAGE          PORTS      RESTARTS
# web    running  nginx:alpine   18080:80   0
```

## 5. Check the published port

```bash
curl -s http://127.0.0.1:18080/
```

The container is reachable on the host port. `docker port` shows the mapping:

```bash
docker port usenova-web 80/tcp
# 0.0.0.0:18080
```

Containers created by the agent are named `usenova-<service-name>`.

## 6. Kill and restart

```bash
docker kill "$(docker ps -q --filter name=usenova-web)"
```

The container comes back: Docker restarts it through the container's restart policy
(`--restart on-failure`), and the agent reconciles the service every 60 seconds and keeps tracking
it. Verify both:

```bash
docker ps --filter name=usenova-web          # running again
curl -s http://127.0.0.1:18080/              # serving again
./bin/nova status web                         # STATE running
```

With `--restart no` the container stays down and the agent reports the service as `failed`.

## 7. Logs, stop and remove

```bash
./bin/nova logs web --tail 20      # last 20 log lines
./bin/nova logs web --follow       # stream new lines
./bin/nova stop web                 # stop the container
./bin/nova remove web               # remove the container and forget the service
```

`nova deploy` refuses to redeploy a running service: stop it first.

## Automated smoke test

`scripts/e2e-smoke.sh` runs the whole path above and checks the result:

```bash
./scripts/e2e-smoke.sh                 # build, run every check, clean up
./scripts/e2e-smoke.sh --keep          # keep the agent, control plane and service running
./scripts/e2e-smoke.sh --port 28080    # publish on another host port
./scripts/e2e-smoke.sh --image nginx:1.27-alpine
```

The script builds the binaries, starts the control plane and the agent, deploys a service with a
published port, kills the container and waits for it to come back, reads the logs, removes the
service and prints a summary. Logs of the run stay in `bin/smoke/`.

### Checks and what they mean

| Check | Passes when |
|---|---|
| docker daemon reachable | `docker info` succeeds |
| go toolchain available | `go` is installed |
| image available locally | The image is cached, or could be pulled |
| ports free | `8080`, `9090` and the published port are unused |
| binaries built | `nova`, `usenova-agent` and `nova-control` compile |
| control plane healthy | `GET /health` answers on `:8080` |
| agent local API reachable | The agent API answers on `127.0.0.1:9090` with the token |
| agent registered with control plane | The agent shows up in `GET /api/v1/agents` |
| service deployed | `nova deploy` succeeded |
| service running | The agent reports the service as `running` |
| published port answers | The host port answers with an HTTP response |
| container publishes the port | `docker port` shows the expected mapping |
| container found / killed / restarted | The container exists and comes back after `docker kill` |
| published port answers after restart | The service serves again after the restart |
| agent still tracks the service | The agent still reports the service as `running` |
| service logs retrievable | `nova logs` returns output |
| service removed | `nova remove` deleted the service |

### Common failures

| Symptom | Cause and fix |
|---|---|
| `warning: no API token configured` | The CLI has no token. Export `NOVA_API_TOKEN` (or `USENOVA_API_TOKEN`) or pass `--token`; it must match the agent's `API_TOKEN` |
| `401 unauthorized` | The token does not match `API_TOKEN`, or `API_TOKEN` is unset on the agent |
| `refusing to bind to non-loopback address` | `API_ADDR` must be a loopback address such as `127.0.0.1:9090`; `:9090` and `0.0.0.0:9090` are refused |
| Agent exits with `registration failed after 3 attempts` | The control plane is not reachable. Start `bin/nova-control` and check `CONTROL_PLANE_URL` |
| `deploy not confirmed within 2m0s` | The deploy ran into the CLI timeout. The agent deploys asynchronously — check `nova status <name>` and the agent log for the real error |
| `failed to pull image ...` | The registry was unreachable. The agent pulls the image on every deploy; check the network or pre-pull the image |
| `service web is already running (stop it first to redeploy)` | Redeploying needs `nova stop web` first |
| The published port refuses connections | The mapping is host:container. With `--port 18080:80` the container must listen on 80 |
| The health check is never registered | `--health-port` expects the container port, not the host port |
| The service is `failed` after a crash loop | The container is not restartable (`--restart no`) or its deploy failed. The agent marks it `failed`; deploy it again to retry |
| `address already in use` | The control plane port (8080) and the agent API port (9090) are fixed. Stop whatever is using them or pick another agent port |

## Next Steps

- [Architecture Overview](./architecture.md)
- [CLI Quick Start](../apps/cli/README.md)
- [Agent configuration](../apps/agent/README.md)
- [Stellar Testnet Setup](./stellar-testnet.md)
- [Host-on-Usenova Design Note](./host-on-usenova.md) — planned fleet direction and non-goals
- [Agent Registry Persistence](./agent-registry-persistence.md)
- [Payment Configuration](./payments.md) (coming soon)
- [Troubleshooting](./troubleshooting.md) (coming soon)

## Need Help?

- [GitHub Issues](https://github.com/Usenova-Labs/usenova-backend/issues)
- [Contributing Guide](../CONTRIBUTING.md)

---

**Note**: This project is in early MVP stage. The local path described above is the one that works
end to end today; remote agents and payment flows are still being built.
