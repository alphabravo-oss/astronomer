# Charlie live qualification hook

The Charlie qualification hook is an operator-started test binary for proving
integration safety against a real Astronomer deployment. It is not linked into
the Astronomer server, has no production route, binds only to an explicit
loopback IP, requires TLS 1.3 and a strong bearer token, and serializes scenarios
so two destructive qualification transitions cannot overlap.

The hook can change the Charlie feature or authority mode and can scale the
Charlie product-agent StatefulSet. Run it only in a dedicated qualification
window. Every invocation requires the exact acknowledgement
`I_UNDERSTAND_CHARLIE_LIVE_EFFECTS`; each implemented scenario captures its
baseline and attempts a bounded restoration before returning.

## Build and configure

```sh
go build -o ./bin/charlie-qualification-hook ./cmd/charlie-qualification-hook
```

Store all tokens, the TLS private key, and kubeconfig in regular files readable
only by their owner. Prefer the `_FILE` variants for secrets. Required settings:

| Setting suffix | Purpose |
| --- | --- |
| `EFFECTS_ACK` | Exact live-effects acknowledgement above. |
| `LISTEN` | Explicit loopback address; defaults to `127.0.0.1:9443`. |
| `TLS_CERT_FILE`, `TLS_KEY_FILE` | Server certificate and owner-only private key. |
| `HOOK_TOKEN_FILE` | Strong bearer token used by the qualification runner. |
| `ADMIN_TOKEN_FILE` | Short-lived Astronomer administrator token. |
| `ASTRONOMER_URL` | HTTPS Astronomer API base URL. HTTP is rejected except loopback when explicitly enabled. |
| `METRICS_URLS` | Comma-separated HTTPS Prometheus endpoints used for before/after counters. |
| `METRICS_TOKEN_FILE` | Optional metrics bearer token. |
| `KUBECONFIG_FILE` | Owner-only kubeconfig required by scenarios that scale the agent. |
| `AGENT_NAMESPACE`, `AGENT_STATEFULSET` | Exact product-agent workload target. |

`DENIED_TOKEN_FILE`, `CA_FILE`, `KUBECTL`, and `COUNTER_METRICS_FILE` are
optional. The counter mapping file is bounded JSON whose keys must match the
hook's fixed runtime/downstream inventory and whose values must be valid
Prometheus metric names. URLs may not contain credentials, queries, fragments,
or redirects.

Start the hook locally:

```sh
ASTRONOMER_CHARLIE_QUALIFICATION_EFFECTS_ACK=I_UNDERSTAND_CHARLIE_LIVE_EFFECTS \
ASTRONOMER_CHARLIE_QUALIFICATION_TLS_CERT_FILE=/secure/hook.crt \
ASTRONOMER_CHARLIE_QUALIFICATION_TLS_KEY_FILE=/secure/hook.key \
ASTRONOMER_CHARLIE_QUALIFICATION_HOOK_TOKEN_FILE=/secure/hook.token \
ASTRONOMER_CHARLIE_QUALIFICATION_ADMIN_TOKEN_FILE=/secure/admin.token \
ASTRONOMER_CHARLIE_QUALIFICATION_ASTRONOMER_URL=https://astronomer.example \
ASTRONOMER_CHARLIE_QUALIFICATION_METRICS_URLS=https://metrics.example/metrics \
./bin/charlie-qualification-hook
```

The binary logs only stable event/failure codes. It never logs token values,
URLs, resource names, request bodies, response bodies, or remote errors.

## Protocol and current coverage

- `GET /v1/counters` returns the complete bounded runtime and downstream-boundary
  counter set.
- `POST /v1/scenarios/{scenario}` accepts schema
  `charlie.live-scenario/v1`, a `qualification-*` run ID, the same scenario name,
  and immutable candidate identifiers/digests.
- Every request requires `Authorization: Bearer <hook token>`. Request bodies are
  limited to 64 KiB, unknown JSON fields are rejected, and responses contain
  only named boolean assertions.

The live driver currently implements `feature_false`, `unactivated`,
`central_disabled`, `emergency_disabled`, and `read_denial`. Other names in the
versioned assertion catalog deliberately return `passed: false`; they are
reserved qualification contracts, not simulated successes. A release record
may check only a scenario whose complete assertion set passed and whose
before/after counters prove the required absence of side effects.

Stop the hook immediately after qualification and revoke/delete its short-lived
tokens and test TLS material.
