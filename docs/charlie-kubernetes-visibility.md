# Charlie Kubernetes visibility in Astronomer

Astronomer can optionally expose bounded Kubernetes diagnostics for the
Astronomer management plane to Charlie. This is implemented inside Astronomer's
existing Product MCP adapter. The generic Charlie product agent remains
Kubernetes-credential-free and Charlie Central remains a separate deployment.

This connector never targets an Astronomer-managed downstream cluster. Existing
Astronomer cluster-agent connection state is management-plane telemetry; it does
not become a Kubernetes transport.

## Administration

Open **Settings → Charlie → Kubernetes**. The visibility profile is independent
from Charlie's `disabled`, `read_only`, `approval`, and `auto` authority mode.

| Profile | Disclosed Kubernetes tools | Not disclosed |
| --- | --- | --- |
| `disabled` | None | Every management Kubernetes read and write |
| `product_namespace` | Owned workloads, pods, rollout status, events, storage, services/network policies, pod metrics, jobs/CronJobs, DaemonSets, HPA/PDB, and ingress/endpoint readiness in the Astronomer namespace | Node inventory and all other namespaces |
| `cluster_diagnostics` | Everything in `product_namespace` plus bounded management-cluster node version/capacity/condition metadata | Arbitrary kinds, selectors, resources, and downstream clusters |

Pod log tails are a separate content opt-in for either enabled profile. They
require an exact pod and container discovered from the owned inventory, have
line/byte/time bounds, and pass the existing redactor. Secret values are never
returned.

Astronomer deliberately does not advertise Charlie's generic
`extended_diagnostics` profile because Astronomer has no additional v1 content
classes that satisfy the contract yet.

## Hard boundary

Every profile fixes these values:

- `product_owned_only=true`;
- `downstream_targets=false`;
- `secret_values=false`;
- `exec=false` and `attach=false`;
- `port_forward=false`;
- `api_proxy=false`; and
- no caller-selected namespace, kind/GVR, URL, wildcard, or label selector.

Targets come from Astronomer's installation namespace and static capability
catalog. The Product MCP re-reads the persisted profile during discovery,
immediately before execution, and during post-verification. The profile gates
both Kubernetes reads and the already allowlisted management-side remediation
tools. Charlie mode, live Astronomer RBAC, exact action tickets, resource scope,
budgets, cooldowns, fencing, and write receipts remain additional independent
requirements.

## Change and acknowledgement lifecycle

Profile changes use a revision-checked, fail-closed state machine:

1. Astronomer writes the profile, closes/drains the distributed Charlie write
   fence, sets both requested and verified mode to disabled, and clears both
   prior disclosure acknowledgements.
2. Astronomer calls its local Product Bridge over mTLS. It binds the request to
   the integration ID and Charlie revision reported by the agent.
3. The product agent signs and forwards rediscovery. Charlie Central compiles
   the new Product MCP catalog, drops stale automatic allowlist entries, stores
   the integration disabled, audits the mutation, and returns a content-free
   candidate receipt.
4. Astronomer persists the candidate digest as `review_required`. The digest is
   shown to an administrator, but the catalog does not cross this configuration
   path.
5. A Charlie configuration administrator reviews the catalog and may
   intentionally change mode or allowlists before acknowledging it. Charlie's
   resulting active disclosure is authoritative and can therefore differ from
   the candidate. Astronomer's normal bridge reconciliation imports it.
6. An Astronomer administrator acknowledges that active digest in the Mode tab.
   This completes the connector lifecycle as `ready`. Only then may normal mode
   prerequisites permit a higher authority request.

The Kubernetes tab reports the stages separately:

- `requires_rediscovery`: the signed bridge call has not completed;
- `requires_central_review`: Charlie has the candidate, but has not activated
  the reviewed disclosure; and
- `requires_product_acknowledgement`: Charlie accepted it, but Astronomer's
  independent acknowledgement is still missing.

If rediscovery fails after the local profile commit, the new local scope remains
enforced and authority remains disabled. Saving the unchanged profile retries
the signed request. A stale browser revision, changed installation, malformed
receipt, central revision regression, or concurrent update is rejected.

## API

The global-admin, `charlie:manage` endpoints are feature gated:

```text
GET /api/v1/admin/charlie/kubernetes-visibility/
PUT /api/v1/admin/charlie/kubernetes-visibility/
```

The update accepts only:

```json
{
  "profile": "cluster_diagnostics",
  "pod_logs": false,
  "revision": 12
}
```

The response contains effective boundary flags, supported profiles, candidate
digest state, and the new revision. It never contains Kubernetes objects,
credentials, Secret names, central credentials, or capability arguments.

## Verification requirements

Changes to this connector must keep these tests green:

- feature-off/non-execution and no-agent-service-account assertions;
- profile-specific discovery and execution rechecks;
- connector provenance and disclosure-digest changes;
- management namespace and bounded node access;
- pod-log content opt-in, redaction, and response limits;
- downstream cluster, Secret, exec/attach/port-forward/proxy negative matrices;
- mode changes and profile changes during admitted work;
- rediscovery mTLS, device signature, installation/revision binding, replay, and
  content-free receipt checks;
- central review and product acknowledgement separation;
- audit-before-mutation and write-fence drain behavior; and
- generated OpenAPI, SQL, Product Bridge, and frontend type drift gates.

Operational failures use the normal Charlie diagnostics and
[Charlie integration runbook](runbooks/charlie-integration.md). Never work around
rediscovery by giving the product agent a service account or by calling Charlie
Central directly from Astronomer.
