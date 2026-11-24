-- Attachment bytes, per session, keyed by their sha256.

CREATE TABLE blobs (
    session_id  TEXT  NOT NULL,
    id          TEXT  NOT NULL,
    data        BYTEA NOT NULL,
    PRIMARY KEY (session_id, id)
);
