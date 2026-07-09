# Dual-tunnel matrix: `connect` (legacy hub) vs `connect2` (remotedialer)

Status as of Wave B (SEC-R01 / DEBT-R01 prep). **Install default remains legacy
`connect`.** Cutover to `connect2` is **not** done.

## Defaults

| Surface | Command / path | Default? |
| --- | --- | --- |
| Agent install manifest | `astronomer-agent connect` | **Yes** — `deploy/agent/install.yaml.template` container `command` is `["astronomer-agent", "connect"]` |
| Agent CLI | `connect` | Legacy full-handler tunnel |
| Agent CLI | `connect2` | Documented as **preferred** for new ad-hoc use (`cmd/agent/main.go`), but not the install default |
| Management routes | `/api/v1/ws/agent/tunnel/{cluster_id}/` | Legacy hub WebSocket |
| Management routes | `/api/v1/connect/...` | remotedialer (`internal/tunnel2`) |

## Why install stays on `connect`

1. **Auth parity (SEC-R01)** — Previously tunnel2 only accepted registration
   tokens and lacked the hub A3 adoption gate / durable hashed-token path.
   Shared validation now lives in `internal/tunnel/connectauth` and both
   surfaces use it. Auth parity is a **prerequisite**, not a complete feature
   matrix.
2. **Locator / multi-replica HA** — Legacy hub publishes `cluster_id → pod`
   into Redis (`internal/tunnel.Locator`) so sibling replicas can reverse-proxy
   to the owner. remotedialer (`tunnel2`) tracks **local sessions only**; there
   is no cross-pod locator. Multi-replica management planes that route
   arbitrarily will 503 for clusters whose agent is connected elsewhere.
3. **Feature coverage** — The hub JSON protocol still owns exec, logs, state
   mirror, helm/internal RPCs, apiserver-audit framing, desired-state pull, and
   related originators. remotedialer ferries dials (strong for k8s API proxy /
   similar) but does not replace every hub message type yet.

## Auth matrix (post SEC-R01)

| Check | Hub (`connect`) | tunnel2 (`connect2`) |
| --- | --- | --- |
| Registration token, cluster match | Yes | Yes |
| Registration token after durable **adopted** (replay) | **Denied** (A3) | **Denied** (shared `connectauth`) |
| Registration token pre-adoption / re-import after re-mint | Allowed | Allowed |
| Durable agent token (hash / grace previous) | Yes | Yes |
| Mint / rotate durable on CONNECT_ACK | Yes | N/A (remotedialer has no CONNECT_ACK token channel) |
| Shared per-IP connect failure limiter | Yes | Yes (same limiter instance when wired) |

## Feature matrix (honest)

| Capability | Hub `connect` | remotedialer `connect2` |
| --- | --- | --- |
| Agent auth (reg + durable, A3 gate) | Yes | Yes (SEC-R01) |
| K8s API proxy / dial-through | Yes | Yes (primary strength) |
| Multi-replica locator HA | Yes | **No** |
| Exec / logs consumers (ticket WS) | Yes (hub streams) | Not on remotedialer path |
| Heartbeat / metrics / state frames | Yes | Different model |
| Desired-state pull, lifecycle ops | Yes | Not ported |
| Install template default | **Yes** | No |

## Cutover decision

**Keep install on `connect` until the feature matrix is green**, including at
minimum:

- SEC-R01 auth parity (done for accept/deny + adoption gate)
- Locator HA for tunnel2 **or** an explicit sticky/single-owner ops contract
- Port or dual-path for exec/logs and any install-critical RPCs
- Dual-run metrics and a deprecation timeline for the hub

Do **not** flip `install.yaml.template` to `connect2` solely because the CLI
marks it preferred.

## Related code

- Install: `deploy/agent/install.yaml.template` (`command: connect`)
- CLI: `cmd/agent/main.go` (`connect` / `connect2`)
- Hub: `internal/tunnel`
- Shared auth: `internal/tunnel/connectauth`
- remotedialer: `internal/tunnel2`
- Advisor tracking: `advisor-plans/001-residual-enterprise-ha-ssrf-parity-master-plan.md` (SEC-R01, DEBT-R01)
