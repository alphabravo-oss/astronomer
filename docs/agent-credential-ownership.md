# Agent credential ownership

The registration manifest and running agent own different Kubernetes Secret
objects. This boundary is intentional and must not be collapsed into two keys
on one object.

| Secret | Owner | Purpose | Mutation path |
| --- | --- | --- | --- |
| `astronomer-agent-registration-token` | installer field manager `astronomer-bootstrap` | Short-lived first-connect/recovery bootstrap | Server-side manifest apply only |
| `astronomer-agent-token` | agent ServiceAccount | Durable adopted identity and rotations | Agent create/update only |

Always apply the registration manifest with:

```bash
kubectl apply --server-side --field-manager=astronomer-bootstrap -f -
```

The durable Secret is absent from rendered YAML. Reapply before or after the
registration token expires therefore cannot overwrite or delete durable state,
and client-side `last-applied-configuration` annotations are not created by the
supported install command. Existing installations already using
`astronomer-agent-token` migrate in place: startup reads that object first and
continues using it.

At startup the agent reads `astronomer-agent-token`. A valid value is preferred
even if bootstrap material was recreated. Only Kubernetes `NotFound` permits
bootstrap fallback. Forbidden, timeout, malformed data, and other read errors
fail closed. Diagnostics report `credential_source=durable_secret`,
`bootstrap_secret`, or `environment` without reporting credential material.
The server remains authoritative for expiry and cluster binding; a wrong-cluster
durable credential is rejected and is never replaced with bootstrap implicitly.

## Kubernetes RBAC create limitation

Kubernetes RBAC cannot restrict `create` by `resourceNames`: create
authorization occurs before an existing resource name can be selected. The
generated Role therefore has two deliberately separate rules:

- `get`, `update`, and `patch` are restricted to
  `resourceNames: ["astronomer-agent-token"]`;
- `create` is the minimum unscoped Secret verb required for first adoption.

The credential Role grants no list, watch, or delete and cannot read or change
any other existing Secret. Explicit `operator` and `admin` privilege profiles
retain their separately documented broader Secret access; this credential Role
does not widen the default `viewer` profile. Environments requiring enforcement
of the create name must add
a fail-closed admission policy (Gatekeeper, Kyverno, or an equivalent validating
webhook) that permits Secret CREATE by
`system:serviceaccount:astronomer-system:<agent-service-account>` only when
`object.metadata.namespace == "astronomer-system"` and
`object.metadata.name == "astronomer-agent-token"`. Apply and test that policy
before installing the agent; do not replace it with broad Secret write access.

Kubernetes 1.25 remains supported, so the bootstrap manifest does not embed the
newer ValidatingAdmissionPolicy API, which is unavailable across the full
supported version range. This documented external admission guard is the
portable compensation for clusters that require named-create enforcement.
