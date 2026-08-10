CREATE TABLE charlie_finding_projection_cursors (
    connection_id UUID PRIMARY KEY REFERENCES charlie_connections(id) ON DELETE CASCADE,
    sequence      BIGINT NOT NULL DEFAULT 0 CHECK (sequence >= 0),
    last_error_code VARCHAR(64) NOT NULL DEFAULT '',
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
