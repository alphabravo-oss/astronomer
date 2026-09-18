# Curated tools and operator governance

The cluster Tools page installs pinned official Longhorn and NeuVector charts on adopted clusters using the normal audited tool-operation workflow. It exposes validated chart settings plus the YAML editor. Longhorn keeps the current default StorageClass and uses `Retain` for new volumes; prepare Linux nodes with open-iscsi and mount propagation before installation. NeuVector requires privileged Linux enforcers and container-runtime access. Its manager stays internal by default; configure authentication before exposing it.

Chart coordinates and field paths were checked against the official [Longhorn charts](https://github.com/longhorn/charts) and [NeuVector charts](https://github.com/neuvector/neuvector-helm). Versions are pinned in migration `042_curated_cluster_tools`.

Istio is not offered as a one-click tool yet. Its official Helm installation requires ordered base and control-plane releases. The existing tool-operation model persists one release and one installed-chart record; safely supporting Istio requires durable multi-release install, upgrade, recovery, adoption and uninstall semantics. A base-only entry would not install a functioning mesh.

In Settings → Platform → Account retention and read audit, superusers can change:

- `users.inactive_retention_days`: 1–3650 days, default 90. The daily leader-elected sweep deactivates inactive human users, revokes credentials and records per-user audit events atomically. Last successful login is used, or account creation if the user has never logged in. Superusers and service accounts are excluded. Invalid settings or database failures stop the sweep without deactivating users. There is no separate deployment-environment retention setting.
- `audit.read_tier`: `standard` uses existing policies; `diagnostic` adds a 10% sampling floor for all authenticated GET/HEAD requests; `incident` adds full coverage. Existing higher-sampling policies retain their coverage. Health and metrics exclusions still apply, and bodies are never captured. This uses the existing bounded read-audit emitter: overload drops are counted by `astronomer_read_audit_emissions_total`, so monitor that metric during incidents.

These settings use the transactional, superuser-only `/api/v1/admin/settings/` API. A write cannot commit without its audit intent. Read-tier changes propagate through the settings cache within 30 seconds; retention changes apply to the next daily sweep. Return from incident mode after investigation to control audit volume.
