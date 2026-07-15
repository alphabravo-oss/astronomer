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
lists, or watches Secrets. Full decommission may delete only the exact bootstrap,
active, legacy, and CA Secret names.

At startup a valid active identity always wins. A current-layout pod is marked
by the explicit `ASTRONOMER_IDENTITY_TOKEN_SECRET_NAME` Deployment env. Only an
empty identity bearing the expected purpose label may then fall through: first
to the exact legacy Secret, then to the exact bootstrap Secret. Missing identity
containers, malformed identity, and non-NotFound reads fail closed.

The built-in self-upgrader changes only the Deployment image. During that
image-first window, the new binary can still see the old
`ASTRONOMER_AGENT_TOKEN` env but no explicit identity-layout marker. It attempts
active identity first. Only `NotFound` or `Forbidden` from that exact read permits
the explicitly marked legacy-layout process to read and rotate the exact legacy
Secret under old RBAC. Other API errors fail closed. Rotation stays on legacy
storage and source until the current manifest is applied and the pod restarts;
that current-layout restart migrates legacy material after server acceptance.
This is not a generic error downgrade. Kubernetes-hosted agents never consume
the token env value itself; it is only an old-layout marker. Environment token
fallback exists only for off-cluster compatibility.

Legacy material migrates only after the server returns `Accepted=true`, which
proves cluster binding. Already-durable legacy material may migrate when the ACK
token is empty or unchanged. Bootstrap adoption instead requires a nonempty,
distinct durable token in the accepted ACK; missing or reused bootstrap material
is rejected without persistence. Wrong-cluster rejection never writes. Known
`kubectl.kubernetes.io/last-applied-configuration` annotations are removed from
new and legacy identity with a static merge-null patch; their contents are never
copied or logged. Cached old manifests can continue changing the ignored legacy
Secret and the legacy `astronomer-agent-token` Role/Binding. The current
credential Role/Binding uses the distinct `astronomer-agent-identity` name, so
cached old apply cannot revoke active-identity authorization or affect active
token data.

## Kubernetes RBAC contract

The generated credential Role grants:

- exact-name `get` for bootstrap, active identity, legacy identity, and the
  agent CA used by guarded decommission;
- exact-name `patch` for active identity and legacy annotation cleanup;
- exact-name `delete` for bootstrap, active identity, legacy identity, and the
  agent CA during full decommission.

It grants no Secret `create`, `update`, `list`, or `watch`, and no arbitrary-name
Secret delete. This design follows real API-server evidence: SSA PATCH cannot
create an absent Secret without create permission, so the installer must
pre-create the empty identity container. No arbitrary Secret write or
compensating admission policy is needed.

Reproduce the fresh/current/cached-old/image-first RBAC and SSA matrix against a
disposable namespace on a real API server with:

```bash
AGENT_IDENTITY_TEST_CONTEXT=<dedicated-context> make verify-agent-identity-live
```

The enterprise gate runs the same script only when that context variable is
explicitly set; otherwise it reports a skip and never touches the current
kubectl context.

## Singleton writer contract

The generated Deployment intentionally uses `replicas: 1` and `strategy:
Recreate`. Credential persistence assumes one agent writer for a cluster. Do not
manually scale the Deployment or run duplicate agents with the same cluster
identity: concurrent accepted ACKs become last-writer-wins on `data.token`, and
one replica can retain a credential the other has already replaced. Recreate
terminates the old writer before an image or manifest rollout starts the next.

## Tunnel compatibility

The deployed `connect` command owns bootstrap adoption, durable handoff, and
rotation. `connect2` is experimental and has no ACK credential channel; it
fails closed unless startup selected `credential_source=durable_identity`.
