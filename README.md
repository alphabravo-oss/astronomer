# Astronomer

<p align="center">
  <strong>Enterprise Kubernetes Operations for Adopted Clusters</strong>
</p>

<p align="center">
  Adopt the clusters you already run. Govern them with policy. Deploy through Argo CD. Operate everything from one control plane.
</p>

<p align="center">
  <a href="#license"><img src="https://img.shields.io/badge/License-AGPL--3.0-blue" alt="License: AGPL-3.0"></a>
  <a href="#platform-capabilities"><img src="https://img.shields.io/badge/Kubernetes-Multi--Cluster-326CE5" alt="Kubernetes multi-cluster"></a>
  <a href="#gitops-delivery"><img src="https://img.shields.io/badge/GitOps-Argo%20CD-EF7B4D" alt="Argo CD GitOps"></a>
  <a href="#declarative-management"><img src="https://img.shields.io/badge/API-CRD%20Native-5B5FC7" alt="CRD native API"></a>
  <a href="#enterprise-foundation"><img src="https://img.shields.io/badge/Built%20For-Day--2%20Operations-111827" alt="Day-2 operations"></a>
</p>

---

Astronomer is a self-hosted Kubernetes management plane for platform teams that need the operational depth of a modern enterprise cluster manager without taking over cluster provisioning.

It is built for organizations that already create clusters with Terraform, Cluster API, cloud consoles, RKE2/k3s, EKS, AKS, GKE, bare metal, or any other provisioning stack, then need a single place to adopt, secure, observe, automate, and operate them.

Astronomer is intentionally day-2 focused:

- No cloud node-pool provisioning.
- No machine drivers.
- No forced infrastructure workflow.
- No inbound access required to managed clusters.
- Argo CD, not Fleet, is the deployment engine.
- Postgres stores product state, audit history, identity, credentials, and durable operations.
- Kubernetes, CRDs, and Argo CD handle declarative desired state and reconciliation.

## Product Promise

Astronomer gives platform teams one trusted operating layer for every cluster after it exists.

| Outcome | What Astronomer Delivers |
| --- | --- |
| Adopt clusters safely | Rancher-style cluster registration, agent install manifests, privilege profiles, registration timelines, and health state. |
| Standardize every cluster | Built-in platform baselines, Argo CD ApplicationSets, curated tools, policy bundles, and consistent labels across the fleet. |
| Operate with confidence | Cluster explorer, workload actions, logs, shell, service proxy, health summaries, events, and live resource views. |
| Govern at scale | Projects, RBAC, SSO/OIDC, TOTP, API tokens, group mappings, audit logs, compliance baselines, and scoped permissions. |
| Secure the control plane | Secret redaction, encrypted credentials, token hashing, NetworkPolicy, TLS posture, least-privilege agents, and high-risk route controls. |
| Prove resilience | HA chart defaults, external Postgres/Redis production posture, management-plane backups, restore drills, queue visibility, and runbooks. |

## Why Astronomer

### Bring Your Own Clusters

Astronomer starts where cluster provisioning ends. Teams keep the infrastructure workflow they already trust, then adopt clusters into Astronomer for governance, visibility, deployment, and day-2 operations.

### Argo-Native Delivery

Astronomer uses the built-in Argo CD installation as the fleet deployment layer. New clusters can be registered into Argo CD automatically, labeled with Astronomer-owned targeting metadata, and managed through ApplicationSets for baseline components, tools, and GitOps-driven workloads.

### Secure Agent Connectivity

Managed clusters run a lightweight agent that connects outbound to the management plane. That model avoids opening inbound firewall paths to every cluster while still enabling Kubernetes API proxying, log streaming, service proxying, health reporting, and controlled operations.

### Enterprise Governance

Astronomer treats policy, RBAC, audit, identity, and secret handling as core product surfaces. High-risk operations are permission-gated, auditable, and designed around durable operation records rather than invisible one-off mutations.

### Dense Operator Experience

The UI is designed for people who live in Kubernetes every day: fast navigation, deep resource views, cluster context, workload controls, Argo CD state, compliance posture, and operational settings without a marketing landing page between the user and the work.

## Platform Capabilities

### Multi-Cluster Operations

- Adopt existing Kubernetes clusters through the dashboard, API, CLI-oriented manifest flow, GitOps source, or Kubernetes-native CRDs.
- Track cluster phase, connectivity, health, labels, projects, environment, region, provider, distribution, Kubernetes version, agent version, and privilege profile.
- Browse live Kubernetes resources across clusters, including namespaces, nodes, events, pods, deployments, daemonsets, statefulsets, jobs, cronjobs, services, ingresses, Gateway API resources, storage, RBAC objects, network policies, quotas, PDBs, ConfigMaps, Secrets, CRDs, and more.
- Run common workload operations such as inspect, scale, restart, edit, and delete where permissions allow.
- Stream pod logs and open controlled shell sessions for approved clusters.
- Proxy approved in-cluster services through Astronomer without exposing every internal dashboard to the network.
- Surface cluster adoption progress, failures, retries, and baseline application state.

### GitOps Delivery

- Built-in Argo CD integration for applications, projects, repositories, syncs, health, resources, and operational state.
- Automatic Argo CD managed-cluster registration for adopted clusters.
- Argo CD cluster Secret labeling contract for deterministic ApplicationSet targeting.
- Platform baseline fan-out through Argo CD ApplicationSets.
- Sync-wave conventions for namespaces, CRDs, operators, policies, workloads, and health checks.
- Drift, orphan, stale resource, and ownership visibility for Argo-managed components.
- GitOps registration sources for declarative cluster onboarding.
- Helm catalog, OCI catalog, curated tools, and baseline component installation paths.

### Security And Governance

- Email-first local authentication with secure browser sessions.
- OIDC/OAuth provider support, SSO presets, group mappings, and logout flows.
- TOTP enrollment, recovery codes, API tokens, token revocation, password reset, and account security workflows.
- Project and cluster-scoped RBAC with permission-aware UI behavior.
- Agent privilege profiles: `viewer`, `operator`, `namespace-viewer`, `namespace-operator`, `custom`, and `admin`.
- Audit logging for material reads, writes, secret access, service proxy mutations, Argo operations, RBAC changes, and admin actions.
- Read-audit policy surfaces for sensitive access monitoring.
- Compliance baseline workflows for common enterprise profiles.
- Secret-handling policy covering hashing, encryption, external references, redaction, support bundles, CRDs, and Argo resources.
- Vault connection surfaces for externally managed secret material.
- Network policy, pod security, image vulnerability, CIS, registry, and service mesh posture surfaces.

### Observability And Operations

- Platform-wide health summary for multi-cluster visibility.
- Monitoring backend configuration and shared observability stack workflows.
- Alerting, notification templates, SMTP settings, outbound webhooks, and SIEM forwarders.
- Logging pipeline configuration and management-plane log tailing.
- Support bundle export with redaction controls.
- Queue and dead-letter inspection for background jobs.
- Durable task-outbox visibility for committed work that has not yet reached the worker queue.
- Management-plane backup, restore-drill status, and disaster-recovery runbooks.
- OpenAPI documentation for supported public APIs.

### Declarative Management

Astronomer exposes Kubernetes-native management APIs under `management.astronomer.io/v1alpha1` when CRDs are enabled.

| CRD | Purpose |
| --- | --- |
| `Cluster` | Declarative adopted-cluster metadata, project refs, Argo adoption intent, baseline profile, agent settings, and ownership metadata. |
| `Project` | Project policy intent, resource quotas, network-policy posture, pod-security posture, and cluster membership. |
| `ClusterBaseline` | Desired baseline profile, target selectors, bundle list, version pins, sync policy, and Argo CD fan-out. |
| `ComponentBundle` | Reusable component definitions, source references, default namespaces, values schema references, health checks, and upgrade policy. |
| `AgentProfile` | Agent privilege, namespace scope, install metadata, capability claims, and RBAC posture. |
| `GitOpsTarget` | Declarative ApplicationSet generation, cluster selectors, project selectors, bundle references, parameters, sync windows, and status. |

The CRD layer is intentionally not a database mirror. It is a Kubernetes-native intent and reconciliation surface for operators who want `kubectl apply` workflows, GitOps-managed platform state, and status conditions without losing the product history and auditability stored in Postgres.

## Enterprise Foundation

Astronomer is designed around a clean split of responsibility:

- Postgres is the durable product database for users, sessions, RBAC, audit history, projects, cluster inventory, credentials, and operation records.
- Redis/asynq is the queue and scheduler layer, not the durable source of operator intent.
- Kubernetes and Argo CD own declarative deployment convergence.
- Target clusters remain the source of truth for live Kubernetes objects.
- CRDs expose operator intent where Kubernetes-native workflows are the right fit.
- Agents execute cluster-local work through authenticated, scoped, audited channels.

That split matters. It lets Astronomer deliver enterprise UX, history, identity, and audit controls without pretending that Postgres should replace etcd or that Argo CD should become an account database.

## Deployment Posture

Astronomer ships as a Kubernetes-native management plane with a Helm chart for development, testing, and production environments.

Production-oriented chart capabilities include:

- Server, worker, and frontend replica controls.
- PodDisruptionBudgets, anti-affinity, and topology spread.
- NetworkPolicy with explicit ingress and egress boundaries.
- Ingress/Gateway and TLS integration.
- cert-manager and Let's Encrypt compatibility.
- External Postgres and Redis support for production.
- Bootstrap admin credential generation or operator-provided password.
- Non-root security contexts, dropped capabilities, seccomp, and read-only-root-filesystem posture where practical.
- Migration, preflight, backup, and restore-drill jobs.
- Management-plane backup to S3-compatible storage.
- Production value validation and chart render tests.

The bundled Postgres and Redis profiles are for development, CI, and small smoke environments. Real production installs should use managed or HA Postgres, TLS, backups, restore drills, and separate protection for encryption and signing keys.

## What Astronomer Is Not

Astronomer is not a cluster provisioning product. It does not try to replace Terraform, Cluster API, cloud provider provisioning, RKE2/k3s installation workflows, or your existing infrastructure automation.

Astronomer is the enterprise operating plane after those clusters exist.

## Getting Started

For local development:

```bash
make dev
```

For a Kubernetes install:

```bash
helm upgrade --install astronomer ./deploy/chart \
  --namespace astronomer \
  --create-namespace \
  -f deploy/chart/values.yaml
```

For production, layer the production values file and provide external Postgres, external Redis, TLS, bootstrap credentials, encryption keys, and backup settings:

```bash
helm upgrade --install astronomer ./deploy/chart \
  --namespace astronomer \
  --create-namespace \
  -f deploy/chart/values.yaml \
  -f deploy/chart/values-production.yaml
```

After installation, retrieve the bootstrap password when one was generated by the chart:

```bash
kubectl -n astronomer get secret astronomer-bootstrap \
  -o jsonpath='{.data.password}' | base64 -d
```

## Operator Experience

Typical first workflows:

1. Log in with the bootstrap admin email.
2. Configure external identity, SSO providers, group mappings, and RBAC.
3. Register an existing cluster and apply the generated agent manifest.
4. Choose the right agent privilege profile for the cluster.
5. Enable Argo CD adoption for baseline and GitOps delivery.
6. Apply a platform baseline or declarative `ClusterBaseline`.
7. Use the cluster explorer, workload actions, logs, shell, tools, security, monitoring, and audit surfaces for day-2 operations.

## Repository Map

| Path | Purpose |
| --- | --- |
| `cmd/server` | Go API server and management-plane entrypoint. |
| `cmd/worker` | Background worker for durable operations and reconciliation tasks. |
| `cmd/agent` | Adopted-cluster agent for outbound connectivity and cluster-local execution. |
| `cmd/astro` | CLI-oriented helper surface. |
| `frontend` | Next.js dashboard. |
| `internal` | Product domains, handlers, workers, CRD controllers, RBAC, auth, audit, tunnel, and database access. |
| `deploy/chart` | Helm chart for the management plane. |
| `deploy/agent` | Agent install manifest rendering. |
| `docs` | Architecture notes, APIs, policies, runbooks, threat model, and implementation plans. |
| `scripts` | Validation, smoke, code-health, OpenAPI, load-test, and operational helpers. |

## Documentation

- [Helm chart and production posture](deploy/chart/README.md)
- [Cluster registration API](docs/cluster-registration-api.md)
- [CRD-based management API](docs/crd-api.md)
- [Control-plane state contract](docs/control-plane-state-contract.md)
- [Agent privilege profiles](docs/agent-privilege-profiles.md)
- [Secret handling policy](docs/secret-handling-policy.md)
- [Threat model](docs/threat-model.md)
- [Operator runbooks](docs/runbooks/README.md)
- [OpenAPI specification](docs/openapi.yaml)

## License

Copyright 2024-2026 AlphaBravo, Inc.

Astronomer is licensed under the GNU Affero General Public License v3.0 or later. See [LICENSE](LICENSE) for details.
