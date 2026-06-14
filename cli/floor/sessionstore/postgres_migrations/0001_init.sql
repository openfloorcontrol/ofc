-- Initial schema for the Postgres SessionStore backend.
-- Three tables: sessions (meta + monotonic seq counter), events (the
-- log), event_refs (per-agent visibility join).

CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,
    meta        JSONB,
    next_seq    BIGINT      NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE events (
    id            BIGSERIAL  PRIMARY KEY,
    session_id    TEXT        NOT NULL,
    seq           BIGINT      NOT NULL,
    room_id       TEXT        NOT NULL DEFAULT '',
    private       BOOLEAN     NOT NULL DEFAULT false,
    payload_type  TEXT        NOT NULL,
    payload       JSONB       NOT NULL,
    time          TIMESTAMPTZ NOT NULL,
    UNIQUE(session_id, seq)
);

CREATE INDEX events_session_seq_idx ON events(session_id, seq);

CREATE TABLE event_refs (
    event_id  BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    agent_id  TEXT   NOT NULL,
    PRIMARY KEY (event_id, agent_id)
);

CREATE INDEX event_refs_agent_idx ON event_refs(agent_id, event_id);
