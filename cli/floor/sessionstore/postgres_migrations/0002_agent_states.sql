-- Per-agent state beside the conversation: what an external (ACP) agent
-- needs to resume a session.

CREATE TABLE agent_states (
    session_id      TEXT   NOT NULL,
    agent_id        TEXT   NOT NULL,
    acp_session_id  TEXT   NOT NULL DEFAULT '',
    sent_seq        BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (session_id, agent_id)
);
