# Architecture decisions

Architecture decision records (ADRs) capture decisions that constrain multiple
Astronomer components or define a durable product boundary. An ADR describes the
decision and its consequences; operational procedures remain in `docs/runbooks`
and machine-enforced release contracts remain under `deploy/release`.

| Decision | Status | Summary |
| --- | --- | --- |
| [Flux-native delivery](decisions/flux-native-delivery.md) | Accepted | Astronomer owns delivery intent, placement, rollout, and status while local Flux controllers converge each managed cluster. Rancher Fleet and Argo are not part of the v1 runtime. |

## Conventions

- Use one Markdown file per decision under `docs/architecture/decisions`.
- State whether the decision is proposed, accepted, superseded, or rejected.
- Name authoritative stores and trust boundaries explicitly.
- Link machine-readable contracts instead of copying values that can drift.
- Add a superseding link to an older ADR rather than silently rewriting its
  historical context.
