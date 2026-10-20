# Agent Registry Persistence

How the control plane persists its agent registry, and how the schema is
bootstrapped.

## Why SQLite

The agent registry used to live only in memory, so every control-plane
restart dropped all registered agents (issue #7). The registry is a single
low-write table read by a single process, so SQLite is the simplest option:

- No external database server to run or configure.
- Pure-Go driver (`modernc.org/sqlite`) — no cgo, builds stay portable.
- One file on disk, trivial to back up, inspect, or reset.

Postgres remains a future option (`DATABASE_URL` is already reserved in
`.env.example`); the store only depends on `database/sql`, so swapping drivers
is contained to the bootstrap code in
`apps/control-plane/internal/agent/db.go`.

## Configuration

| Variable                | Default               | Description                              |
| ----------------------- | --------------------- | ---------------------------------------- |
| `CONTROL_PLANE_DB_PATH` | `data/control-plane.db` | Path to the SQLite database file.        |

The parent directory is created on startup if it does not exist. The `*.db`
pattern is already gitignored.

## Schema

Defined in `apps/control-plane/internal/agent/schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS agents (
    agent_id          TEXT PRIMARY KEY,
    hostname          TEXT NOT NULL,
    version           TEXT NOT NULL,
    capabilities      TEXT NOT NULL DEFAULT '{}',
    last_heartbeat    TEXT NOT NULL,
    registration_time TEXT NOT NULL,
    session_token     TEXT NOT NULL
);
```

- `capabilities` is JSON (currently `{"docker": bool}`).
- `last_heartbeat` and `registration_time` are RFC3339 timestamps.
- Last reported status (uptime, running services) is intentionally **not**
  stored: it is ephemeral and refreshed by the agent's next heartbeat.

## Migration / bootstrap path

There is no separate migration command. Bootstrap happens on every startup:

1. `agent.OpenSQLite(path)` opens (or creates) the database file, creates the
   parent directory, and applies the embedded `schema.sql` via `agent.Migrate`.
2. The schema is applied with `CREATE TABLE IF NOT EXISTS`, so it is
   idempotent — a fresh checkout, an existing database, and a restart all
   converge on the same schema.
3. `agent.NewPersistentAgentStore(db)` loads every persisted agent into
   memory before the HTTP server starts serving.

Because the schema file is embedded with `go:embed`, the bootstrap path ships
inside the binary — no loose SQL files are needed at deploy time.

### Adding schema changes later

Edit `schema.sql` using idempotent DDL (`ALTER TABLE ... ` guarded as needed,
or additive `CREATE TABLE IF NOT EXISTS`). When a change can no longer be
expressed idempotently, introduce a `schema_migrations` version table (or a
versioned `migrations/` directory) in `db.go`; the bootstrap entry point stays
the same.

## Restart behavior

- Registered agents (id, hostname, version, capabilities, session token,
  registration time, last heartbeat) are reloaded before the API accepts
  traffic — the registry survives control-plane restarts.
- `status` on an agent resets to zero until its next heartbeat.
- The HTTP API is unchanged: `POST /api/v1/agents/register`,
  `POST /api/v1/agents/:agentID/heartbeat`, `GET /api/v1/agents`, and
  `GET /api/v1/agents/:agentID` behave exactly as before.

## Resetting the registry

Stop the control plane, delete the database file, and start it again:

```bash
rm -f data/control-plane.db
```

The schema is re-created and the registry starts empty.
