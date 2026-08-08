# Playbook: Tunnel or fleet agent connection flaps

**Version:** 0.3.5 · **Maturity:** test-run

## Goal

Diagnose management-plane tunnel/hub issues and **fleet agent connectivity
metadata** without entering downstream clusters.

## Tools (in order)

1. `astronomer.tunnel.health`
2. `astronomer.tunnel.replica_distribution`
3. `astronomer.tunnel.recent_errors` (optional `connection_id`, `since`, `limit`)
4. `astronomer.agent_fleet.summary` then `list` / `get` for a specific `cluster_id`
5. `astronomer.agent_fleet.connection_history`
6. `astronomer.agent_fleet.ingestion_health` and `upgrade_status` if upgrades suspected

## Decision

| Mode | Action |
| --- | --- |
| `read_only` | Prefer diagnosis + findings only |
| `approval` | `astronomer.tunnel.restart_component` only with exact args after evidence |
| `auto` | Tunnel restart usually **not** allowlisted; fall back to approval/finding |

Never claim to have checked downstream pod state. Only connection/telemetry held
by Astronomer is in scope.

## Operator-only follow-ups (do not execute via Charlie)

- Downstream node network, firewall, agent logs on the customer cluster
- Credential reissue procedures outside the disclosed catalog
