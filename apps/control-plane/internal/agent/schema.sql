-- Agent registry schema for the control plane.
-- Applied idempotently at startup (see db.go); safe to run repeatedly.
CREATE TABLE IF NOT EXISTS agents (
    agent_id          TEXT PRIMARY KEY,
    hostname          TEXT NOT NULL,
    version           TEXT NOT NULL,
    capabilities      TEXT NOT NULL DEFAULT '{}',
    last_heartbeat    TEXT NOT NULL,
    registration_time TEXT NOT NULL,
    session_token     TEXT NOT NULL
);
