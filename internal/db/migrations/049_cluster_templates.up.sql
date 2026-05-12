-- Cluster templates (migration 049).
--
-- Operator-managed "Production Web App" style templates that pre-package
-- the manual cluster onboarding flow: environment + labels + tool installs
-- + a default project + a registration-token rotation policy. Applying a
-- template to a cluster is idempotent and convergent — re-running yields
-- the same end state with no side effects.
--
-- The spec is JSONB so the shape can evolve (new tool fields, new policy
-- knobs) without a per-field migration. The handler validates the body at
-- write time: unknown top-level keys are rejected, enum fields
-- (environment, pod_security_profile) are checked against fixed
-- allow-lists. See internal/handler/cluster_templates.go for the
-- canonical shape.
--
-- Expected spec.fields (validated at the handler layer; the DB is
-- intentionally permissive so a future schema version doesn't require a
-- migration):
--
--   environment: "production" | "staging" | "development"
--   labels: { k: v, ... }   -- merged into clusters.labels at apply time
--   tools: [
--     { slug: "argocd",       preset: "ha",      values: { … } },
--     { slug: "cert-manager", preset: "default" },
--     …
--   ]
--   default_project: {
--     name: "platform",
--     pod_security_profile: "baseline",       -- privileged|baseline|restricted
--     resource_quota_cpu_limit: "8",
--     resource_quota_memory_limit: "16Gi",
--     network_policy_mode: "isolated"
--   }
--   registration_policy: {
--     token_rotation_days: 90                  -- 0/absent = inherit default
--   }

CREATE TABLE cluster_templates (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Unique human name. The /cluster-templates list endpoint groups by
    -- this column for the picker UI; UNIQUE keeps the picker free of
    -- ambiguous duplicates and lets the create handler 409 cleanly.
    name        VARCHAR(128) NOT NULL UNIQUE,
    description TEXT         NOT NULL DEFAULT '',
    -- The full template body. JSONB rather than per-field columns so
    -- adding a new tool override knob (say "wait_timeout_seconds") doesn't
    -- require yet another ALTER TABLE on a hot table.
    spec        JSONB        NOT NULL DEFAULT '{}',
    -- SET NULL on user delete so audit history (and the picker name)
    -- survives an admin departure. Matches the cluster + project pattern.
    created_by  UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Per-cluster applied-template tracking. A cluster can have AT MOST ONE
-- applied template at a time — re-applying updates this row in place.
-- The PRIMARY KEY on cluster_id encodes that 1:1 relationship and gives
-- the lookup (per cluster detail page) an automatic index.
CREATE TABLE cluster_template_applications (
    cluster_id    UUID        PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    -- ON DELETE RESTRICT: deleting a template fails when at least one
    -- cluster still references it. The DELETE handler returns 409 with a
    -- "remove this template from N clusters first" message, the standard
    -- pattern across the rest of the API (charts, projects, …).
    template_id   UUID        NOT NULL REFERENCES cluster_templates(id) ON DELETE RESTRICT,
    -- "pending" | "applying" | "applied" | "failed"
    --
    -- The handler writes 'pending' on the initial POST and enqueues an
    -- apply task. The worker transitions to 'applying' as it starts and
    -- 'applied' or 'failed' at the end. Reapply re-uses the same row.
    status        VARCHAR(16) NOT NULL DEFAULT 'pending',
    -- Snapshot of the template spec at apply time. Drift detection
    -- compares the LIVE cluster state against this snapshot — if the
    -- operator later edits cluster_templates.spec, the snapshot stays
    -- as-applied so we can tell the operator "the template definition
    -- changed; click reapply to converge".
    spec_snapshot JSONB       NOT NULL DEFAULT '{}',
    last_error    TEXT        NOT NULL DEFAULT '',
    applied_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Reverse-lookup index for the template DELETE handler ("is this template
-- still in use anywhere?") and the cluster-template detail view that
-- shows every cluster currently using the template.
CREATE INDEX idx_cluster_template_applications_template
    ON cluster_template_applications (template_id);

-- Per-cluster registration-policy stamp. The apply worker writes this
-- when spec.registration_policy.token_rotation_days is set; the
-- existing cleanup_registration_tokens task can later read it to decide
-- when to rotate. Stored separately from cluster_template_applications
-- so detaching a template doesn't blow away the policy by accident; the
-- handler explicitly deletes both rows when the operator detaches.
CREATE TABLE cluster_registration_policies (
    cluster_id          UUID        PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    -- 0 means "no rotation policy enforced"; positive values are read by
    -- the token-rotation task. INTEGER (not SMALLINT) so the field can
    -- carry generous policy windows without future expansion.
    token_rotation_days INTEGER     NOT NULL DEFAULT 0,
    -- The template that stamped this policy. NULL when an operator
    -- writes a one-off policy out of band (future use).
    source_template_id  UUID        REFERENCES cluster_templates(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
