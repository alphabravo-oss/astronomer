# Astronomer

### The unified control plane for Kubernetes fleets, built by AlphaBravo

> This document is a marketing content library, not a single fixed pitch. It is organized so that any section can be lifted on its own for a landing page, a one-pager, a slide, an email, or a sales conversation. Start at the top for high-level positioning, then pull from the deep dives, persona pages, use cases, and proof points as the audience requires.

---

## Table of contents

1. Positioning and elevator pitches
2. The problem we solve
3. Capabilities at a glance
4. The platform pillars (deep dives)
   - Fleet management and cluster adoption
   - GitOps delivery and continuous reconciliation
   - The secure agent architecture
   - The tools catalog and platform baseline
   - Security and continuous posture
   - Image scanning and supply chain visibility
   - Observability: metrics, logs, and monitoring
   - Intelligent alerting and anomaly detection
   - Multi-tenancy, projects, and quotas
   - Identity, single sign-on, and access control
   - Network policy and segmentation
   - Service mesh and mutual TLS
   - Policy enforcement and guardrails
   - Secrets management
   - Backup, restore, and disaster recovery
   - Cluster templates and standardization
   - GitOps application delivery with a native experience
   - Workloads, resources, and live cluster operations
   - Audit, compliance, and evidence
   - Notifications, webhooks, and integrations
   - Search and fleet-wide discovery
5. Value by role
6. Use cases and scenarios
7. What makes Astronomer different
8. Architecture overview
9. Where Astronomer runs
10. Outcomes and business impact
11. Frequently asked questions
12. Glossary
13. About AlphaBravo and boilerplate

---

## 1. Positioning and elevator pitches

**One line.**
Astronomer is the unified control plane that lets teams adopt, secure, observe, and operate their entire Kubernetes fleet from one place.

**One sentence.**
Astronomer brings governance, GitOps delivery, security, and observability together into a single console, so platform teams can run many clusters as consistently and safely as they run one.

**Fifty words.**
Astronomer is a Kubernetes fleet control plane from AlphaBravo. It adopts the clusters you already run and manages all of them through one secure console. It does not provision clusters; teams keep their existing provisioning workflow and bring clusters into Astronomer for day-2 operations. GitOps delivery keeps clusters consistent, a lightweight agent keeps them reachable without exposed APIs, and built-in security and observability keep them safe and visible.

**One hundred words.**
Astronomer, built by AlphaBravo, is a unified control plane for Kubernetes fleets. Instead of stitching together kubeconfigs, dashboards, and one-off scripts, teams adopt and operate every cluster from a single console with one consistent workflow. A lightweight in-cluster agent connects outbound over a single tunnel, so clusters are managed without exposing their APIs. GitOps delivery continuously reconciles both applications and the platform baseline, so clusters never drift from their intended state. Security, image scanning, network policy, multi-tenancy, observability, intelligent alerting, backup, and enterprise identity are built in. The secure, governed path becomes the easy path, so teams move quickly without losing control.

**The tagline shortlist.** Pick the one that fits the channel.

- One control plane for every cluster you run.
- Run your whole fleet like it is one cluster.
- Secure by default. Consistent by design.
- Kubernetes at fleet scale, without the chaos.
- The platform team's platform.
- Adopt, secure, observe, operate. From one place.

---

## 2. The problem we solve

Running Kubernetes in production is no longer the hard part. Running many clusters, across many teams, across many environments, and keeping all of them secure, compliant, observable, and consistent, is the hard part.

Most organizations hit the same wall as they grow:

- **Every cluster drifts.** What was installed on one is missing or different on another, and nobody is certain which is correct.
- **Access is inconsistent.** Permissions are granted ad hoc, kubeconfigs proliferate, and no one can answer who can do what, where.
- **Security is an afterthought.** Scanning, policy, and segmentation get bolted on late, if at all, and run on a separate cadence from delivery.
- **Observability is fragmented.** Metrics and logs live in different places per cluster and per vendor, so there is no single picture of fleet health.
- **Onboarding is slow.** Bringing a new cluster, a new team, or a new engineer up to speed takes days of manual setup and tribal knowledge.
- **Toil compounds.** The same change has to be made by hand in many places, which is where outages and incidents are born.

The deeper issue is that most tooling treats the individual cluster as the unit of management. At fleet scale, that is the wrong unit. Astronomer treats the fleet as the unit, and brings delivery, governance, security, and observability into one system so that consistency is the default state rather than a constant fight.

---

## 3. Capabilities at a glance

- **Fleet management.** Adopt existing clusters and provision new ones, then see and operate all of them from one console.
- **GitOps delivery.** Declarative, auditable application and platform delivery with continuous reconciliation.
- **Secure agent architecture.** A lightweight in-cluster agent connects out over a single tunnel, with no inbound firewall holes and no exposed cluster APIs.
- **Tools catalog.** Install and manage platform components through a clean configuration experience with live progress.
- **Security and posture.** Image scanning, network policy, policy enforcement, secrets integration, and continuous posture checks.
- **Observability.** Metrics, logs, monitoring, and intelligent alerting with anomaly detection.
- **Multi-tenancy.** Projects, quotas, network isolation, and per-tenant scoping.
- **Access control.** Role-based access with global, cluster, and project scopes, plus enterprise single sign-on.
- **Resilience.** Scheduled backups, restores, snapshots, and recovery drills.
- **Standardization.** Cluster templates and a platform baseline that keep every cluster consistent.
- **Auditability.** A complete, queryable record of who did what and when.

---

## 4. The platform pillars

Each pillar below is written to stand alone. Every one includes what it is, the key features, why it matters, and how it helps teams, so you can drop any single pillar into a page or a deck without additional context.

### Fleet management and cluster adoption

**What it is.** Astronomer is fleet-first. Adopt a cluster you already run, or stand up a new one, and from that moment it appears in a single inventory alongside everything else you operate. Each cluster carries its real status, node inventory, workloads, installed components, distribution, Kubernetes version, and security posture, all visible without juggling contexts or credentials.

**Key features.**
- Adopt existing clusters in minutes, regardless of where they run.
- A single fleet inventory with live status and health for every cluster.
- Per-cluster detail covering nodes, workloads, resources, and posture.
- Cluster groups for organizing the fleet by environment, team, region, or purpose.
- New clusters inherit the platform baseline and governance rules automatically.

**Why it matters.** The cost of Kubernetes is not the first cluster, it is the tenth and the hundredth. When every cluster is visible, consistent, and governed from day one, the marginal cost of growth drops dramatically. Teams stop maintaining a mental map of which cluster has what, because the platform maintains it for them.

**How it helps teams.** New environments come online faster. Audits become a query instead of an investigation. On-call engineers can reason about any cluster in the fleet because they all behave the same way.

### GitOps delivery and continuous reconciliation

**What it is.** Astronomer delivers both applications and platform components through a declarative GitOps model. Desired state lives in version control. The platform continuously reconciles each cluster toward that state, detects drift, and corrects it. Changes are reviewed, recorded, and reversible.

**Key features.**
- Continuous reconciliation of applications and platform components.
- Automatic drift detection and correction.
- The platform baseline itself is delivered through GitOps, so adopted clusters converge to a known-good configuration.
- Full change history with straightforward rollback.
- The review discipline you already use for code now governs infrastructure.

**Why it matters.** Manual changes are the root cause of most outages and most security incidents. A reconciled, declarative system removes whole categories of failure: no more snowflake clusters, no more undocumented hotfixes, no more wondering whether staging and production actually match.

**How it helps teams.** Every change has a paper trail. Rollbacks are trivial. New clusters are reconciled into shape rather than configured by hand. The same approval workflow that protects your code now protects your fleet.

### The secure agent architecture

**What it is.** Astronomer connects to managed clusters through a lightweight agent that runs inside each cluster and establishes an outbound tunnel back to the control plane. The cluster never exposes its API server to the network, and no inbound ports need to be opened. The control plane drives operations across that tunnel, while internal services are isolated by network policy so that only the components that need to reach a cluster can.

**Key features.**
- Outbound-only connectivity, so no inbound firewall exceptions are required.
- No exposed cluster API server and no need for VPNs or bastion hosts.
- Network isolation treated as a real security boundary between control plane components.
- Agent health, version, and diagnostics surfaced in the console, with self-tests and guided remediation.
- Selectable privilege profiles so each agent runs with only the access it needs.
- Defined behavior when an agent is offline, including which operations safely queue until it reconnects.

**Why it matters.** Every inbound path to a cluster API is a target. By inverting the connection so the cluster reaches out, Astronomer removes that attack surface entirely. Clusters in private networks, behind NAT, or in restricted environments can be managed without opening them up.

**How it helps teams.** Security teams get a smaller, clearer attack surface. Operations teams get reach into clusters that would otherwise be painful to access. Compliance teams get an architecture that is straightforward to explain and defend.

### The tools catalog and platform baseline

**What it is.** Platform components, the things that make a cluster useful and safe, are delivered through a curated catalog. Each tool installs with a clean configuration experience that surfaces the settings that actually matter, such as scaling, storage, resources, and networking, with full control available for advanced cases. Installs stream live progress so you can watch exactly what is being applied.

**Key features.**
- A curated catalog of platform components with guided, form-based configuration.
- Sensible defaults for common settings, with full low-level control when needed.
- Live, streaming install progress instead of opaque background jobs.
- A minimal, opt-in baseline so the platform can observe every cluster, with everything beyond that left to your choice.
- Bring your own components. If you already run an ingress controller, a logging stack, or a scanner, Astronomer respects it.
- Reconciled lifecycle, so installed components stay in their intended state.

**Why it matters.** Most platforms force a false choice between an opinionated stack you cannot change and a blank slate that gives you nothing. Astronomer gives you a strong default and a clear, governed path to extend it, without lock-in.

**How it helps teams.** Standing up a capable platform stops being a long exercise in raw manifests. Teams adopt the components they want, configure them through one consistent interface, and keep them reconciled. Existing investments are respected rather than overwritten.

### Security and continuous posture

**What it is.** Security is woven through Astronomer rather than bolted on. The platform continuously watches for weak configurations, broad privileges, and drift across the fleet, and surfaces what it finds where teams can act on it.

**Key features.**
- Continuous posture checks across every cluster in the fleet.
- Least-privilege agent profiles, with broad access flagged as an advisory.
- Centralized network policy and segmentation.
- Policy enforcement that validates workloads before they reach production.
- Integration with external secret stores.
- Image scanning surfaced alongside the workloads it affects.

**Why it matters.** Security that lives in a separate tool, on a separate cadence, run by a separate team, is security that gets skipped. By making posture a first-class, always-on property of the fleet, Astronomer turns security from a periodic audit into a continuous state.

**How it helps teams.** Developers get feedback early. Security teams get fleet-wide visibility without chasing individual clusters. Leadership gets confidence that the same standards apply everywhere.

### Image scanning and supply chain visibility

**What it is.** Astronomer surfaces vulnerabilities in the images running across your clusters, presented where teams can prioritize and act on them rather than buried in a separate tool.

**Key features.**
- Vulnerability findings tied to the workloads and clusters they affect.
- Fleet-wide visibility into what is running and what is at risk.
- A clear, non-disruptive experience for teams that prefer a different scanner, including how to re-enable native scanning at any time.
- Support for hardened and trusted image sources.
- Image registry and pull-secret management per cluster.

**Why it matters.** The software supply chain is one of the most exploited paths into production. Knowing exactly which images are running, and which carry known vulnerabilities, is the difference between fixing a problem on your schedule and discovering it on an attacker's.

**How it helps teams.** Security and platform teams get a single, current view of supply chain risk across the fleet. Remediation is prioritized by real exposure rather than guesswork.

### Observability: metrics, logs, and monitoring

**What it is.** Astronomer ships with the foundations of observability enabled across the fleet, so every adopted cluster reports metrics from the moment it joins. Teams get monitoring views, log access, and a consistent observability story across every environment.

**Key features.**
- Fleet-wide metrics enabled by default on every adopted cluster.
- Monitoring views for cluster, node, and workload health.
- Centralized log access, with log forwarding and pipelines available through the catalog.
- Customizable dashboards and widgets for the views each team cares about.
- One consistent observability experience instead of a different setup per cluster.

**Why it matters.** You cannot operate what you cannot see. Observability that is consistent across the fleet means a problem looks the same wherever it happens, which makes it faster to detect, diagnose, and resolve.

**How it helps teams.** On-call engineers get a uniform picture of health. Teams stop reinventing monitoring per environment. Leadership gets a real-time view of the fleet rather than a patchwork of disconnected tools.

### Intelligent alerting and anomaly detection

**What it is.** Astronomer includes an alerting engine that goes beyond static thresholds. It supports anomaly detection driven by rolling baselines, so it can flag behavior that is unusual for a given workload rather than only firing when a fixed number is crossed. Alerts route to the notification channels teams already use.

**Key features.**
- Threshold-based rules for the cases where a hard limit is the right tool.
- Anomaly rules that learn a rolling baseline per workload and alert on deviation.
- Configurable sensitivity, windows, direction, and cooldowns.
- Routing to multiple notification channels.
- Alert silences for planned maintenance.

**Why it matters.** Static thresholds are blunt. They fire late, they fire falsely, and they require constant tuning. Baseline-aware alerting catches the problems that matter, the slow leak, the creeping latency, the workload behaving unlike itself, before they become incidents.

**How it helps teams.** Less alert fatigue, earlier warning, and fewer pages that turn out to be noise. The signal teams act on is more often a real signal.

### Multi-tenancy, projects, and quotas

**What it is.** Astronomer models the real structure of an organization. Projects carve a cluster into governed tenants, each with its own namespaces, resource quotas, network isolation mode, and policy. Cloud credentials and catalogs can be scoped per project, so teams get exactly the access and resources they should have, and nothing more.

**Key features.**
- Projects as governed, isolated tenants on shared clusters.
- Per-project resource quotas, limit ranges, and pod count caps.
- Per-project network isolation modes and policy.
- Per-project cloud credentials and catalogs.
- Quota usage visibility so capacity is managed, not guessed.

**Why it matters.** Shared clusters without governance become noisy, insecure, and contested. Strong multi-tenancy lets many teams share infrastructure safely, which improves utilization and lowers cost, while keeping each team isolated and accountable.

**How it helps teams.** Platform teams hand out self-service capacity without handing out the keys to everything. Application teams get a clear, bounded space to work in. Finance gets better utilization of the clusters they already pay for.

### Identity, single sign-on, and access control

**What it is.** Astronomer integrates with enterprise identity through single sign-on using OIDC and SAML, with connector configuration, group mapping, and granular roles. Access decisions are centralized and consistent, and they follow the same model across every cluster in the fleet.

**Key features.**
- Single sign-on with OIDC and SAML.
- Identity connector configuration and self-service registration flows.
- Group mappings from your identity provider to platform roles.
- Role-based access control with global, cluster, and project scopes.
- A least-privilege model that is enforceable rather than aspirational.

**Why it matters.** Identity sprawl is a security liability and an operational drag. Centralized, standards-based authentication means access is granted and revoked in one place, mapped from the groups you already manage.

**How it helps teams.** Onboarding and offboarding are immediate and complete. Access reviews are straightforward. The principle of least privilege becomes the default rather than the exception.

### Network policy and segmentation

**What it is.** Astronomer lets you define and apply network isolation across clusters from a central place, so workloads are segmented by default rather than by exception.

**Key features.**
- Centralized definition and application of network policies.
- Per-project isolation modes for tenant separation.
- Consistent segmentation across the fleet rather than per-cluster guesswork.
- Visibility into network access posture.

**Why it matters.** Flat networks let a single compromise spread. Default-deny segmentation contains blast radius, which is one of the highest-leverage things you can do for security.

**How it helps teams.** Security teams get consistent isolation they can verify. Application teams get safe defaults without having to become network policy experts.

### Service mesh and mutual TLS

**What it is.** Astronomer supports service mesh capabilities including mutual TLS, so service-to-service traffic can be authenticated and encrypted.

**Key features.**
- Service mesh integration surfaced in the console.
- Mutual TLS for authenticated, encrypted service-to-service communication.
- Fleet-level visibility into mesh and mTLS posture.

**Why it matters.** Encryption and identity inside the cluster, not just at the edge, are increasingly required by zero trust and compliance mandates. Mutual TLS ensures services prove who they are and that traffic between them cannot be read in transit.

**How it helps teams.** Teams meet zero trust and regulatory requirements for in-cluster traffic without hand-rolling certificate management.

### Policy enforcement and guardrails

**What it is.** Astronomer applies guardrails that ensure what runs in your clusters meets your standards before it ever reaches production, with policy baselines you can apply across the fleet.

**Key features.**
- Policy enforcement that validates workloads against your standards.
- Compliance baselines that can be applied fleet-wide.
- Pod security profiles and templates.
- Consistent guardrails across every cluster.

**Why it matters.** Guardrails catch the misconfiguration before it ships, which is far cheaper and safer than catching it in an incident review. Consistent policy across the fleet means standards are actually standard.

**How it helps teams.** Developers get fast, clear feedback when something does not meet policy. Security and compliance teams get assurance that the rules are enforced everywhere, not just where someone remembered.

### Secrets management

**What it is.** Astronomer integrates with external secret stores so sensitive material is never scattered across manifests.

**Key features.**
- Integration with external secret management.
- Sensitive values kept out of plain manifests and version control.
- Consistent secrets handling across the fleet.

**Why it matters.** Secrets in plain manifests are one of the most common and most damaging mistakes in Kubernetes. Centralized secret management removes that exposure.

**How it helps teams.** Developers reference secrets safely without copying them around. Security teams get a single place to manage and rotate sensitive material.

### Backup, restore, and disaster recovery

**What it is.** Astronomer provides scheduled backups, on-demand backups, restores, and snapshots, with storage targets you control. It also supports recovery drills, so you can prove that your backups actually restore.

**Key features.**
- Scheduled and on-demand backups with configurable retention.
- Restores and point-in-time snapshots.
- Backup storage configuration with targets you control.
- Recovery drills to validate that backups restore as expected.
- Backup and restore history surfaced in the console.

**Why it matters.** A backup you have never restored is a hope, not a plan. Built-in scheduling and drills turn disaster recovery from a document into a tested, repeatable capability.

**How it helps teams.** Recovery objectives become real and measurable. The team that has rehearsed a restore is the team that stays calm when it counts.

### Cluster templates and standardization

**What it is.** Cluster templates capture a known-good configuration, the components, policies, and settings a cluster should have, so that new and adopted clusters converge to the same standard. Consistency stops being a manual checklist and becomes a property of the system.

**Key features.**
- Reusable templates that define a cluster's intended components and settings.
- Automatic convergence for new and adopted clusters.
- A single place to evolve the standard and roll it across the fleet.

**Why it matters.** Drift is inevitable when consistency depends on people remembering. Templates make the standard executable, so every cluster starts from the same baseline and stays there.

**How it helps teams.** Provisioning is repeatable. Audits are simpler because clusters are uniform. The gap between what is intended and what is actually running closes.

### GitOps application delivery with a native experience

**What it is.** Beyond the platform baseline, Astronomer gives teams a full GitOps application delivery experience, including managing applications, application sets, repositories, and projects, with access to the underlying delivery engine's native interface when deep control is needed.

**Key features.**
- Manage applications and application sets across the fleet.
- Repository and delivery-project management.
- Sync status, health, and history at a glance.
- Direct access to the native delivery interface for advanced workflows.

**Why it matters.** GitOps is the proven model for safe, auditable delivery. Bringing it into the same console as the rest of fleet operations means delivery and operations are not two separate worlds.

**How it helps teams.** Application teams ship through a reviewed, reconciled pipeline. Platform teams see delivery and infrastructure health side by side.

### Workloads, resources, and live cluster operations

**What it is.** Astronomer lets teams browse and operate live cluster contents, including workloads, custom resources, nodes, and arbitrary resources, with secure in-cluster access when hands-on work is required.

**Key features.**
- Browse workloads by kind, namespace, and cluster.
- Inspect nodes, custom resources, and any resource type.
- Per-workload metrics and detail.
- Secure in-cluster shell access for hands-on operations, governed by access control.

**Why it matters.** Real operations sometimes require looking inside a cluster. Doing that through a governed, audited console is safer than handing out kubeconfigs and hoping for the best.

**How it helps teams.** Engineers get the access they need to do real work, within guardrails, with every action attributable.

### Audit, compliance, and evidence

**What it is.** Astronomer keeps a complete, queryable record of activity across the fleet, including a record of reads where that level of scrutiny is required, plus compliance baselines and the evidence to back them up.

**Key features.**
- A full audit trail of who did what and when.
- Optional read auditing for high-scrutiny environments.
- Compliance baselines applied across the fleet.
- Evidence that is queryable rather than reconstructed after the fact.

**Why it matters.** When an auditor or an incident asks what happened, the answer should take seconds, not a week. A continuous, structured record turns compliance and investigation from a scramble into a query.

**How it helps teams.** Compliance teams get evidence on demand. Security teams can reconstruct exactly what happened. Leadership gets accountability without micromanagement.

### Notifications, webhooks, and integrations

**What it is.** Astronomer routes important events to the channels and systems teams already use, through notification channels, webhooks, and configurable delivery and templates.

**Key features.**
- Multiple notification channels for alerts and events.
- Webhooks to integrate fleet events into your own systems.
- Configurable payloads, retries, and delivery tracking.
- Email and notification template configuration.

**Why it matters.** A platform that only talks to itself is an island. Pushing the right events to the right systems is what lets Astronomer plug into the way your organization already works.

**How it helps teams.** Events reach people and pipelines where they already pay attention. Automation downstream can react to what happens in the fleet.

### Search and fleet-wide discovery

**What it is.** Astronomer provides search across the fleet, so finding a cluster, a workload, or a resource is a single action rather than a hunt across many tools.

**Key features.**
- Fleet-wide search across clusters and resources.
- Fast navigation to the thing you need.

**Why it matters.** At fleet scale, finding things is its own tax. Good search removes it.

**How it helps teams.** Engineers spend time acting, not locating.

---

## 5. Value by role

**For platform and infrastructure teams.** Operate many clusters with one workflow. Offer self-service to application teams without losing governance. Standardize with templates and a reconciled baseline so consistency is automatic. Reduce toil by making the platform do the repetitive work.

**For security teams.** Shrink the attack surface with an outbound-only agent and no exposed cluster APIs. Enforce least privilege, segmentation, and policy across the whole fleet. Get continuous posture and supply chain visibility instead of point-in-time audits. Have a clear, defensible architecture to present to assessors.

**For application and development teams.** Ship through a reviewed, reconciled GitOps pipeline. Work inside a clear, bounded project with the resources you need. Get early feedback when something does not meet policy. Use observability and alerting that already work, without becoming Kubernetes experts.

**For compliance and risk teams.** Get a continuous, queryable audit trail and compliance baselines applied fleet-wide. Produce evidence on demand. Rely on centralized identity and access that maps to the groups you already govern.

**For leadership.** Grow the fleet without growing headcount linearly. Get consistent security and observability everywhere. Reduce the risk of outages and incidents born from manual change and drift. Improve utilization of the infrastructure you already pay for.

**For managed service providers and multi-customer operators.** Manage many isolated tenants and many clusters from one control plane. Onboard new customers quickly with templates and baselines. Keep each tenant isolated, governed, and auditable, and prove it.

---

## 6. Use cases and scenarios

**Standardizing a sprawling, inconsistent fleet.** Adopt every existing cluster, apply a template and baseline, and let reconciliation bring them all to a known-good state. Drift stops being a recurring firefight.

**Securing clusters in private or restricted networks.** Manage clusters behind NAT, in private subnets, or in air-gapped-leaning environments through the outbound agent, with no inbound exposure and no bastion sprawl.

**Giving teams safe self-service.** Stand up projects with quotas, isolation, and scoped access so application teams move on their own, while the platform team keeps the guardrails.

**Rolling out a security baseline everywhere at once.** Apply policy, segmentation, scanning, and posture checks across the fleet from one place, and verify continuously that they stay applied.

**Proving disaster recovery actually works.** Schedule backups, then run recovery drills to validate restores against real objectives, before an incident forces the question.

**Consolidating observability.** Replace a patchwork of per-cluster monitoring with consistent, fleet-wide metrics, logs, and intelligent alerting.

**Onboarding a new environment or customer.** Spin up or adopt a cluster, attach a template, and have it governed, observable, and secure within minutes.

---

## 7. What makes Astronomer different

- **Fleet-first, not cluster-first.** The unit of management is the fleet, so consistency and governance scale instead of fragmenting.
- **Secure by architecture.** Outbound-only connectivity and no exposed cluster APIs remove an entire class of attack surface, rather than mitigating it after the fact.
- **Opinionated defaults without lock-in.** A strong, minimal baseline plus a clear path to bring your own tools. You are never forced to throw away what already works.
- **One console, many concerns.** Delivery, security, observability, identity, and governance share a single surface, so they reinforce each other instead of living in silos.
- **Reconciled, not just deployed.** GitOps reconciliation means intended state is continuously enforced, so the platform fights drift for you.
- **Built for the real shape of organizations.** Projects, scoped access, and multi-tenancy reflect how teams actually work.

---

## 8. Architecture overview

Astronomer is built around a central control plane and lightweight in-cluster agents.

- **The control plane** is the single console and the brain of the system. It holds the fleet inventory, drives operations, evaluates posture, runs the alerting engine, and serves the unified experience.
- **The agent** runs inside each managed cluster and establishes an outbound tunnel to the control plane. Because the connection originates from the cluster, no inbound ports are opened and the cluster API is never exposed to the network.
- **GitOps reconciliation** continuously aligns each cluster with its declared desired state, for both applications and the platform baseline.
- **Network isolation** separates internal components so that only the parts that need to reach a cluster can, treating isolation as a security boundary rather than a convenience.
- **Privilege profiles** let each agent run with the minimum access required for its role, with broad access surfaced as an advisory rather than hidden.

The result is an architecture that is secure to expose, simple to explain, and able to reach clusters wherever they run.

---

## 9. Where Astronomer runs

Astronomer is designed to manage clusters wherever they live: in public cloud, in private data centers, at the edge, and in restricted or disconnected-leaning networks. Because the agent connects outbound, clusters do not need public endpoints or inbound firewall changes to be managed. One control plane can govern a fleet that spans many providers and many locations, presenting all of it through a single, consistent experience.

---

## 10. Outcomes and business impact

These are the outcomes to lead with when the audience cares about results rather than features.

- **Faster onboarding.** Bring a new cluster, environment, or team online in minutes instead of days.
- **Lower operational cost.** Reduce the manual toil that scales with cluster count, so the team does not grow linearly with the fleet.
- **Fewer incidents.** Eliminate the drift and manual change that cause a large share of outages and security events.
- **Stronger security posture.** Continuous posture, least privilege, segmentation, and a reduced attack surface, applied everywhere at once.
- **Better utilization.** Safe multi-tenancy lets teams share clusters, improving the return on infrastructure you already pay for.
- **Audit readiness.** Evidence and access history available on demand, turning compliance from a project into a query.
- **Resilience you can prove.** Tested backups and rehearsed recovery, not untested hope.

---

## 11. Frequently asked questions

**Do we have to replace our existing tools?**
No. Astronomer ships a minimal baseline and lets you bring your own ingress, logging, scanning, and more. The catalog is opt-in, and existing components are respected.

**Do we need to open inbound firewall ports to our clusters?**
No. The agent connects outbound to the control plane, so clusters are managed without inbound exposure or a public API endpoint.

**Can it manage clusters we already run?**
Yes. Adoption is a core capability and is designed to take minutes. Adopted clusters inherit your baseline and governance automatically.

**Does it work across multiple clouds and locations?**
Yes. One control plane can manage a fleet that spans public cloud, private data centers, and the edge.

**How does it handle access control?**
Through role-based access with global, cluster, and project scopes, integrated with enterprise single sign-on via OIDC and SAML and mapped from your identity provider's groups.

**What happens if an agent goes offline?**
The cluster's last known state remains visible, and the platform defines which operations safely queue until the agent reconnects.

**How are changes tracked?**
Every change is recorded in an audit trail, and GitOps delivery keeps a full, reversible history of intended state.

---

## 12. Glossary

- **Fleet.** The full set of clusters managed by Astronomer, treated as a single unit of management.
- **Control plane.** The central console and engine that manages the fleet.
- **Agent.** The lightweight in-cluster component that connects outbound to the control plane.
- **GitOps.** A delivery model where desired state lives in version control and is continuously reconciled onto clusters.
- **Reconciliation.** The continuous process of aligning a cluster with its declared desired state and correcting drift.
- **Baseline.** The minimal set of platform components enabled so the platform can observe and manage every cluster.
- **Project.** A governed, isolated tenant on a cluster, with its own quotas, isolation, and access.
- **Privilege profile.** The scoped set of permissions an agent runs with, following least privilege.
- **Posture.** The ongoing security and configuration health of the fleet.

---

## 13. About AlphaBravo and boilerplate

**Short boilerplate.**
Astronomer is developed by AlphaBravo, a team focused on secure, production-grade Kubernetes and platform engineering. AlphaBravo builds Astronomer to be secure by default, consistent by design, and respectful of the tools and standards teams already rely on.

**Long boilerplate.**
Astronomer is developed by AlphaBravo. AlphaBravo builds secure, production-grade platforms for organizations that run Kubernetes at scale, including teams in demanding and regulated environments. Astronomer reflects that focus: a fleet control plane that treats security, consistency, and governance as defaults rather than add-ons, while respecting the existing tools and standards its users depend on. The goal is simple, a platform where the secure, governed, observable path is also the easy path, so teams can move quickly without trading away control.

To learn more or to see Astronomer in action, reach out to AlphaBravo.
