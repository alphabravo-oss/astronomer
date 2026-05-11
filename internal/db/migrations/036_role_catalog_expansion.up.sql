-- Expand the built-in RBAC catalog with focused operator/auditor roles that
-- come up in real engagements but weren't covered by 001 + 032.
--
-- Pattern matches 032: idempotent INSERT...WHERE NOT EXISTS so re-running
-- this migration on a partially-seeded database is a no-op for already-
-- present role names.
--
-- All roles use the resource/verb vocabulary defined in internal/rbac/types.go.
-- Verbs in use across the catalog: create, read, update, delete, list, watch,
-- scale, restart, exec, logs, proxy, sync. Resources: clusters, projects,
-- workloads, pods, monitoring, alerts, catalog, backups, security, rbac,
-- settings, argocd, sso, users, audit_logs, agents.

-- ── Global roles ────────────────────────────────────────────────────────
INSERT INTO global_roles (name, description, rules, is_builtin)
SELECT v.name, v.description, v.rules::jsonb, v.is_builtin
FROM (
VALUES
-- Catalog ownership without cluster-admin scope. For platform engineers who
-- curate helm repos and which charts are blessed but should not be granting
-- themselves cluster access.
('Catalog Maintainer',
 'Manage Helm/OCI repositories, available charts, and installed-chart lifecycle across the platform',
 '[{"resource":"catalog","verbs":["create","read","update","delete","list"]},{"resource":"clusters","verbs":["read","list"]}]',
 true),
-- Backup operators who can trigger restores during incidents without being
-- granted broader platform admin.
('Backup Operator',
 'Manage backup storage, schedules, runs, and restores across the platform',
 '[{"resource":"backups","verbs":["create","read","update","delete","list"]},{"resource":"clusters","verbs":["read","list"]}]',
 true),
-- For the cluster registration/onboarding role often filled by a platform
-- team. Registers clusters and rotates agent tokens but doesn't manage
-- workloads inside.
('Cluster Registrar',
 'Register new clusters and manage agent lifecycle; cannot edit cluster workloads',
 '[{"resource":"clusters","verbs":["create","read","update","delete","list"]},{"resource":"agents","verbs":["create","read","update","delete","list"]}]',
 true),
-- Tighter alerting scope for SRE rotations.
('Alerts Manager',
 'Manage alert rules, channels, silences, and events platform-wide',
 '[{"resource":"alerts","verbs":["create","read","update","delete","list"]},{"resource":"monitoring","verbs":["read","list"]},{"resource":"clusters","verbs":["read","list"]}]',
 true)
) AS v(name, description, rules, is_builtin)
WHERE NOT EXISTS (
    SELECT 1 FROM global_roles existing WHERE existing.name = v.name
);

-- ── Cluster roles ──────────────────────────────────────────────────────
INSERT INTO cluster_roles (name, description, rules, is_builtin)
SELECT v.name, v.description, v.rules::jsonb, v.is_builtin
FROM (
VALUES
-- "Edit workloads in this cluster" without the broader Cluster Member
-- scope (no monitoring config, no policy edits).
('Workload Editor',
 'Create, update, scale, and delete workloads in a cluster; read-only elsewhere',
 '[{"resource":"clusters","verbs":["read"]},{"resource":"workloads","verbs":["create","read","update","delete","list","scale","restart"]},{"resource":"pods","verbs":["read","list","watch","logs"]}]',
 true),
-- Incident-response role: pod exec + logs + delete-pod but no other
-- mutating power. Common pattern for first-line on-call.
('Pod Incident Responder',
 'Diagnose and remediate pod-level incidents (logs, exec, delete pods) without broader cluster write access',
 '[{"resource":"clusters","verbs":["read"]},{"resource":"workloads","verbs":["read","list","restart"]},{"resource":"pods","verbs":["read","list","watch","logs","exec","delete"]}]',
 true),
-- Monitoring/alerting ops without the rest of cluster admin.
('Cluster Monitoring Admin',
 'Manage monitoring config and alert rules scoped to a cluster',
 '[{"resource":"clusters","verbs":["read"]},{"resource":"monitoring","verbs":["create","read","update","delete","list"]},{"resource":"alerts","verbs":["create","read","update","delete","list"]}]',
 true)
) AS v(name, description, rules, is_builtin)
WHERE NOT EXISTS (
    SELECT 1 FROM cluster_roles existing WHERE existing.name = v.name
);

-- ── Project roles ──────────────────────────────────────────────────────
INSERT INTO project_roles (name, description, rules, is_builtin)
SELECT v.name, v.description, v.rules::jsonb, v.is_builtin
FROM (
VALUES
-- Read-only inspection of project workloads. Useful for compliance reviewers
-- who shouldn't be able to exec into pods (the existing Project Troubleshooter
-- can exec).
('Project Auditor',
 'Read-only visibility into project workloads, pods, and recent logs; no exec',
 '[{"resource":"projects","verbs":["read"]},{"resource":"workloads","verbs":["read","list"]},{"resource":"pods","verbs":["read","list","watch","logs"]},{"resource":"monitoring","verbs":["read","list"]}]',
 true),
-- CI/CD-style role: can create and update workloads but never delete or
-- scale. Common for deployment pipelines that should not be able to wipe a
-- namespace by mistake.
('Project Deployer',
 'Create and update workloads within a project; cannot delete or scale',
 '[{"resource":"projects","verbs":["read"]},{"resource":"workloads","verbs":["create","read","update","list","restart"]},{"resource":"pods","verbs":["read","list","watch","logs"]}]',
 true)
) AS v(name, description, rules, is_builtin)
WHERE NOT EXISTS (
    SELECT 1 FROM project_roles existing WHERE existing.name = v.name
);
