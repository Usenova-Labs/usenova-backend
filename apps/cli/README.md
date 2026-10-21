# Nova CLI (`nova`)

Command-line interface for the Usenova local agent API. Deploy, inspect, and
stop services running on infrastructure you control.

## Build

```bash
mkdir -p bin   # gitignored; not present on a clean checkout
(cd apps/cli && go build -o ../../bin/nova)
```

Or build everything from the repo root:

```bash
./scripts/build.sh   # produces bin/nova, bin/usenova-agent, bin/nova-control
```

## Configuration

| Setting | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| Agent API URL | `--agent-url` | `USENOVA_AGENT_URL` | `http://127.0.0.1:9090` |
| Bearer token | `--token` | `USENOVA_API_TOKEN` | (empty) |
| Output format | `-o, --output` | — | `text` |
| Request timeout | `--timeout` | — | `2m` |

Security notes:

- The agent API is loopback-only and requires a bearer token.
- The CLI refuses to send a token over plaintext HTTP to a non-loopback host
  (use `https://` or an SSH tunnel to reach a remote agent).
- An empty token is warned about at startup and results in a 401 from the agent.

## Commands

### Deploy

```bash
nova deploy \
  --name web \
  --image nginx:alpine \
  --port 8080:80 \
  --env ORIGINS=a,b --env LOG_LEVEL=info \
  --restart unless-stopped \
  --health-path /health --health-port 8080
```

`--health-path`/`--health-port` configure an agent-side health check
(`health_check` in the service spec; the type is derived as `http` when a path
or port is given, otherwise `tcp`).

`--max-restarts`, `--restart-window` and `--restart-backoff` bound how often
the agent restarts a failing service (defaults come from the agent
configuration). Once the budget is used up the service is marked `failed` and
the agent stops restarting it.

Deployments can outlive `--timeout`; when that happens the CLI queries the
service state and reports it instead of a bare timeout.

### Inspect

```bash
nova status                 # table of all services
nova status web             # detail view for one service
nova status -o json         # raw agent JSON
nova agent status           # ping the agent API
```

Text mode renders an aligned table:

```
NAME    STATE     IMAGE           PORTS    RESTARTS
web     running   nginx:alpine    8080:80  0
```

### Stop / remove

```bash
nova stop web
nova remove web
```

### Logs

```bash
nova logs web               # last 100 lines
nova logs web --tail 500    # last 500 lines (--tail all for everything)
nova logs web --follow      # stream new lines until Ctrl-C
nova logs web --timestamps  # prefix each line with its timestamp
nova logs web -o json       # {"service","count","lines"} document
```

`-o json` reads the selected tail (it cannot be combined with `--follow`).
Clear errors are reported when the service is unknown (404) or Docker is
unavailable on the agent (503).

## Service names

Names must be a single URL path segment: no `/`, no control characters.
Reserved characters such as `?` and `#` are accepted and escaped by the CLI.

## Follow-ups

- [ ] Control plane commands (`nova agents list`, billing, …) are out of scope
  for this MVP.
