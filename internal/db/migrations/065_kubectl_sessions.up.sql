-- Migration 065 — in-browser kubectl sessions.
--
-- Sprint 17 adds the operator-facing "Open Shell" affordance on a
-- managed cluster: the operator clicks once, and the management plane
-- spins up an ephemeral debug pod in the target cluster (image:
-- bitnami/kubectl:1.31), binds it to a short-lived ServiceAccount whose
-- effective RBAC mirrors the operator's verbs against this cluster, and
-- streams `kubectl exec -it` stdin/stdout back through the existing
-- tunnel exec consumer (sprint 14) into an xterm.js terminal.
--
-- Two tables:
--
--   * `kubectl_sessions`            — one row per open shell. Tracks the
--     ServiceAccount + Role + debug-pod names so the reaper can clean
--     them up (orphan SA/Role/Pod sweep if the row was deleted out from
--     under us). status drives the reaper state machine; expires_at is
--     the hard 4-hour cap, last_input_at is the 30-minute idle cap.
--
--   * `kubectl_session_commands`    — heuristic command-line audit log.
--     We record INPUT lines that end in \r or \n (one row each, capped
--     at 1 KB per line) — never output bytes. Output may contain
--     secrets (the operator could legitimately run
--     `kubectl get secret -o yaml`). The compliance story is
--     "operator visibility into who ran what, where, when".

CREATE TABLE kubectl_sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cluster_id      UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    -- The k8s ServiceAccount + Role we created in the cluster for this
    -- session. ON close we delete these. If reaper finds an orphaned
    -- session-named SA/Role, it cleans up.
    sa_namespace    VARCHAR(253) NOT NULL DEFAULT 'kube-system',
    sa_name         VARCHAR(253) NOT NULL,
    -- The debug-pod name we created.
    pod_namespace   VARCHAR(253) NOT NULL DEFAULT 'kube-system',
    pod_name        VARCHAR(253) NOT NULL,
    -- "starting" | "active" | "closed" | "expired" | "failed"
    status          VARCHAR(16) NOT NULL DEFAULT 'starting',
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_input_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at       TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '4 hours'),
    last_error      TEXT NOT NULL DEFAULT '',
    client_ip       INET,
    user_agent      TEXT NOT NULL DEFAULT '',
    CONSTRAINT kubectl_status_valid CHECK (status IN ('starting','active','closed','expired','failed'))
);
CREATE INDEX idx_kubectl_sessions_user ON kubectl_sessions (user_id, started_at DESC);
CREATE INDEX idx_kubectl_sessions_active ON kubectl_sessions (cluster_id, last_input_at DESC) WHERE status IN ('starting','active');
CREATE INDEX idx_kubectl_sessions_reap   ON kubectl_sessions (expires_at) WHERE status IN ('starting','active');

-- Session input log. We record COMMANDS (heuristically, lines ending in \n).
-- Output is NOT logged — too noisy + may contain secrets from `kubectl get
-- secret -o yaml`. The compliance story is "operator visibility into who
-- ran what against which cluster, when".
CREATE TABLE kubectl_session_commands (
    id              BIGSERIAL PRIMARY KEY,
    session_id      UUID NOT NULL REFERENCES kubectl_sessions(id) ON DELETE CASCADE,
    command_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    command_line    TEXT NOT NULL
);
CREATE INDEX idx_kubectl_session_commands_session ON kubectl_session_commands (session_id, command_at);
