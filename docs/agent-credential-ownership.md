# Agent credential ownership

Agent registration uses three distinct Secret names so installer reapply and
agent rotation cannot compete for active credential material.

| Secret | Owner | Purpose | Mutation path |
| --- | --- | --- | --- |
| `astronomer-agent-registration-token` | installer field manager `astronomer-bootstrap` | Short-lived first-connect/recovery bootstrap | Current manifest SSA |
| `astronomer-agent-identity` | installer owns the empty labeled container; agent field manager `astronomer-agent-identity` owns only `data.token` | Active durable identity and rotations | Installer SSA for container fields; exact-name agent SSA PATCH for token |
| `astronomer-agent-token` | legacy installer/agent | Pre-AGENT-02 durable migration input only | Cached old manifests may write it; current agent only reads it and scrubs the known last-applied annotation after accepted migration |

Always apply the current registration manifest with:

```bash
kubectl apply --server-side --field-manager=astronomer-bootstrap -f -
```

The current manifest creates `astronomer-agent-identity` with metadata, type,
and `astronomer.io/agent-credential-purpose=durable-identity-container`, but no
`data.token`. Reapply therefore cannot overwrite active identity. The agent
uses force ownership only on its minimal SSA document containing
`data.token`; it never applies installer labels/type and never creates, updates,
lists, watches, or deletes Secrets.

At startup a valid active identity wins. Only an empty identity bearing the
expected purpose label may fall through: first to the exact legacy Secret, then
to the exact bootstrap Secret. Missing identity containers, malformed identity,
and non-NotFound reads fail closed. Kubernetes-hosted agents do not use the token
environment variable. Environment fallback exists only for explicit off-cluster
compatibility.

Legacy material migrates only after the server returns `Accepted=true`, which
proves cluster binding. The accepted token is patched into the new identity even
when the ACK token is empty or unchanged. Wrong-cluster rejection never writes.
Known `kubectl.kubernetes.io/last-applied-configuration` annotations are removed
from new and legacy identity with a static merge-null patch; their contents are
never copied or logged. Cached old manifests can continue changing the ignored
legacy Secret but cannot affect the active identity name.

## Kubernetes RBAC contract

The generated credential Role grants:

- exact-name `get` for bootstrap, active identity, and legacy identity;
- exact-name `patch` for active identity and legacy annotation cleanup.

It grants no Secret `create`, `update`, `list`, `watch`, or `delete`. This design
follows real API-server evidence: SSA PATCH cannot create an absent Secret
without create permission, so the installer must pre-create the empty identity
container. No arbitrary Secret write or compensating admission policy is needed.

## Tunnel compatibility

The deployed `connect` command owns bootstrap adoption, durable handoff, and
rotation. `connect2` is experimental and has no ACK credential channel; it
fails closed unless startup selected `credential_source=durable_identity`.
