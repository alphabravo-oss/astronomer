# Network policy templates (migration 068)

Pre-built Kubernetes `NetworkPolicy` bundles that operators apply across
clusters and namespaces. Sister-feature of [cluster templates] (sprint
049) — both ship "click to apply" presets, but the two manage different
layers:

| Concept              | Scope                | What it touches                                  |
| -------------------- | -------------------- | ------------------------------------------------ |
| **Cluster template** | One cluster          | `clusters` row (env, labels), tool installs, default project, registration policy |
| **Network policy template** (this doc) | One namespace per cluster | A single `NetworkPolicy` object in that namespace |

A cluster template can pre-seed a default project; a network policy
template sets the namespace's ingress / egress rules. They compose: the
"Production Web App" cluster template might say "install ArgoCD + create
`platform` namespace", and the "Project isolated" network policy
template would then be applied to `platform` to lock down ingress.

[cluster templates]: ../internal/db/migrations/049_cluster_templates.up.sql

## Built-in templates

The migration seeds four `kind='builtin'` rows. Operators cannot edit or
delete these — clone via POST first to get a `kind='custom'` row.

| Slug                          | Use                                                                    |
| ----------------------------- | ---------------------------------------------------------------------- |
| `deny_all_ingress`            | Block all inbound traffic. Layer with explicit allow rules.            |
| `project_isolated`            | Only allow ingress from pods labeled `astronomer.io/project=<this>`.   |
| `namespace_only`              | Only allow ingress from pods in the same namespace.                    |
| `allow_ingress_controllers`   | Permit only ingress controllers (`ingress-nginx`, `traefik`). Egress open. |

## How apply works

1. Operator POSTs `{ template_id, namespace }` (or `namespaces: [...]`)
   to `/api/v1/clusters/{cluster_id}/network-policies/applications/`.
2. The handler inserts one `network_policy_applications` row per
   namespace with `status='pending'` and fires a `network_policy:apply`
   asynq task.
3. The worker renders the template body with
   `{Namespace, Project, PolicyName}`, server-side-applies the YAML
   through the tunnel K8sRequester (`PATCH /apis/networking.k8s.io/v1/
   namespaces/.../networkpolicies/astronomer-np-<slug>`), and marks
   the row `applied`.
4. A 30-minute drift sweep GETs the live object and marks `drifting`
   when the managed-by label disappears; the next apply tick re-stamps.
5. DELETE on the application row revokes the in-cluster
   `NetworkPolicy` *and* removes the row. If the revoke fails, the row
   stays in `status='failed'` so the operator can clean up manually.

## Naming

Each application's `policy_name` is `astronomer-np-<template_slug>` —
the same name on re-apply so SSA converges on one object per template
per namespace. The reconciler ONLY touches objects with this prefix;
operator-authored policies are never edited.

## RBAC

| Endpoint                                                              | Verb   | Resource              |
| --------------------------------------------------------------------- | ------ | --------------------- |
| `/admin/network-policy-templates/`                                    | CRUD   | `network_policies`    |
| `/clusters/{id}/network-policies/applications/`                       | RW     | `clusters` + `update` |

The split mirrors the cluster_templates split: the template library is
admin-only; binding a template to one of your clusters reuses the
`clusters:update` verb you already need to edit the cluster.

## Audit + metrics

* `admin.network_policy_template.{created,updated,deleted}`
* `cluster.network_policy.{applied,reverted}`
* `astronomer_network_policy_apply_total{template, outcome}` counter
* `astronomer_network_policy_applications{cluster, status}` gauge

## v1 scope

* Only ingress + the four built-in templates ship in v1.
* Egress-allowlist templates are a v2 — the schema already carries a
  free-form `spec_template` so adding them later is purely a seed +
  builtin row.
* No "preview YAML before apply" endpoint yet — the handler renders
  once at create time to validate template syntax and on every apply
  in the worker.
