-- Agent registry schema for the control plane.
-- Applied idempotently at startup (see db.go); safe to run repeatedly.
CREATE TABLE IF NOT EXISTS agents (
    agent_id          TEXT PRIMARY KEY,
    hostname          TEXT NOT NULL,
    version           TEXT NOT NULL,
    capabilities      TEXT NOT NULL DEFAULT '{}',
    owner_type        TEXT NOT NULL DEFAULT 'user',
    owner_id          TEXT NOT NULL DEFAULT '',
    labels            TEXT NOT NULL DEFAULT '{}',
    last_heartbeat    TEXT NOT NULL,
    registration_time TEXT NOT NULL,
    session_token     TEXT NOT NULL
);

-- Deployment records link an operator's deploy request to the agent and the
-- remote command that carries it. The service spec itself is not stored:
-- environment variables may contain secrets.
CREATE TABLE IF NOT EXISTS deployments (
    id           TEXT PRIMARY KEY,
    agent_id     TEXT NOT NULL,
    service_name TEXT NOT NULL,
    image        TEXT NOT NULL,
    state        TEXT NOT NULL,
    command_id   TEXT NOT NULL DEFAULT '',
    requester    TEXT NOT NULL DEFAULT '',
    metadata     TEXT NOT NULL DEFAULT '{}',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_deployments_agent_id ON deployments (agent_id);
CREATE INDEX IF NOT EXISTS idx_deployments_command_id ON deployments (command_id);
