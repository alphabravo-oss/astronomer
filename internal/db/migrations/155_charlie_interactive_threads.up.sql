-- Interactive thread continuity: durable user-facing conversation that can span
-- multiple authorized Charlie sessions. Message bodies remain in Charlie.
CREATE TABLE charlie_interactive_threads (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    connection_id      UUID NOT NULL REFERENCES charlie_connections(id) ON DELETE CASCADE,
    owner_user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title              VARCHAR(256) NOT NULL DEFAULT '',
    state              VARCHAR(32) NOT NULL,
    current_session_id UUID REFERENCES charlie_sessions(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at        TIMESTAMPTZ,
    CONSTRAINT charlie_interactive_threads_state_chk CHECK (state IN ('active', 'archived'))
);

-- At most one active interactive thread per user per Charlie connection.
CREATE UNIQUE INDEX charlie_interactive_threads_one_active
    ON charlie_interactive_threads (connection_id, owner_user_id)
    WHERE state = 'active';

CREATE INDEX charlie_interactive_threads_owner_updated
    ON charlie_interactive_threads (owner_user_id, updated_at DESC);

CREATE TABLE charlie_thread_sessions (
    thread_id  UUID NOT NULL REFERENCES charlie_interactive_threads(id) ON DELETE CASCADE,
    session_id UUID NOT NULL REFERENCES charlie_sessions(id) ON DELETE CASCADE,
    sequence   INT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (thread_id, session_id),
    UNIQUE (thread_id, sequence),
    CONSTRAINT charlie_thread_sessions_sequence_positive CHECK (sequence > 0)
);

CREATE INDEX charlie_thread_sessions_session_idx
    ON charlie_thread_sessions (session_id);

-- Correlation only: optional reverse pointer. No content columns.
ALTER TABLE charlie_sessions
    ADD COLUMN IF NOT EXISTS thread_id UUID REFERENCES charlie_interactive_threads(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS charlie_sessions_thread_idx
    ON charlie_sessions (thread_id)
    WHERE thread_id IS NOT NULL;
