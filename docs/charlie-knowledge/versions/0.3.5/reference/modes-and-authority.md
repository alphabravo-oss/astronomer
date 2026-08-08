# Modes and authority (Astronomer 0.3.5)

Authority is **not** chosen by the model. Product mode + disclosure + live RBAC
gate every tool call. Charlie chat sessions inherit the product’s authoritative
mode for that connection.

---

## Modes

| Mode | Product tools | Writes | Typical use in test run |
| --- | --- | --- | --- |
| `disabled` | Off | Off | Connection present but not usable for SRE chat |
| `read_only` | Reads yes | No | **Default for this test install** — diagnose freely |
| `approval` | Reads yes | One exact human-approved write | Remediations after disclosure |
| `auto` | Reads yes | Only auto-eligible + allowlisted | Narrow background remediations |

Raising mode requires Charlie admin configuration, disclosure acknowledged, and
not emergency-disabled. Astronomer UI may still show requested vs verified mode
drift — trust **verified** mode from status APIs.

---

## Disclosure

Operators must acknowledge the product safety disclosure digest before elevated
modes apply. Mismatch → tools stay restricted.

---

## Approval loop (when mode = approval)

1. Read tools gather evidence.
2. Model proposes **exactly one** write with exact arguments.
3. Human approves **once** in the product UI (proposal ≠ execution).
4. After success, re-read (`rollout_status` / `pods` / `readiness`) to verify.
5. Denial or `read_only` → finding / manual steps; never “shop” for a more powerful tool.

---

## Auto-eligible writes (catalog)

Only these writes are marked auto-eligible in product code; both still need
`mode=auto` **and** central allowlist:

- `astronomer.argocd.self_management_sync`
- `astronomer.queue.retry_task`

Restarts, scale, tunnel restart, and run_job are **not** auto-eligible.
