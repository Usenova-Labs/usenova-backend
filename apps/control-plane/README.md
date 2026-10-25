# Usenova Control Plane

Central coordination API that agents register with and report to.

## Endpoints

| Method + path | Auth | Description |
| --- | --- | --- |
| `GET /health` | none | Liveness probe |
| `POST /api/v1/agents/register` | none | Register a BYOI/user agent; issues a session token |
| `POST /api/v1/agents/register/platform` | platform token | Register a platform/fleet agent |
| `POST /api/v1/agents/:agentID/heartbeat` | Bearer | Update agent status |
| `GET /api/v1/agents/:agentID` | Bearer | Get one agent |
| `GET /api/v1/agents` | none | List/filter agents |

`GET /api/v1/agents` accepts `owner_type=user|platform`, `owner_id`, and
repeatable `label=key=value` filters (for example,
`?owner_type=platform&label=region=us-east`). All supplied filters must match.

## Platform registration

The default registration endpoint always creates `owner_type: "user"` agents
and ignores ownership fields in the request body. Platform agents use the
separate endpoint and must send the control-plane bootstrap token configured
in `CONTROL_PLANE_PLATFORM_TOKEN`:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/agents/register/platform \
  -H 'Content-Type: application/json' \
  -H "X-Platform-Bootstrap-Token: $CONTROL_PLANE_PLATFORM_TOKEN" \
  -d '{
    "agent_id": "fleet-agent-1",
    "hostname": "fleet-host-1",
    "version": "1.0.0",
    "owner_id": "fleet-1",
    "labels": {"region": "us-east", "pool": "alpha"}
  }'
```

When no platform token is configured the platform endpoint returns `503`;
missing or invalid tokens return `401`.

## Agent authentication

Registration is the only unauthenticated agent operation: it is where the
credential is first issued. The response includes a random, opaque
`session_token`:

```json
{ "agent_id": "…", "session_token": "64-hex-characters", "message": "Agent registered successfully" }
```

Every subsequent agent-scoped request must send that token as a bearer token:

```
Authorization: Bearer <session_token>
```

Missing or invalid tokens are rejected with `401 Unauthorized`. The agent ID is
taken from the path and the token is validated against the one stored for that
agent, so a token issued to one agent cannot act for another.

The token is compared in constant time. There is no expiry yet; when the store
is backed by persistence the token should be stored only as a hash
(`TODO` in `internal/agent/store.go`).

## Development

```bash
go test ./...
./test-api.sh   # end-to-end smoke test including 401 vs 200 checks
```
