# Architecture decisions

Architecture decision records (ADRs) capture decisions that constrain multiple
Astronomer components or define a durable product boundary. An ADR describes the
decision and its consequences; operational procedures remain in `docs/runbooks`
and machine-enforced release contracts remain under `deploy/release`.

The [technology-guide baseline](../assurance/technology-guide/README.md) records
the October 2026 component, compatibility, ownership, and regression inventory.
Its [decision matrix](../assurance/technology-guide/decision-matrix.md) distinguishes
accepted product alternatives from literal guide prescriptions. Runtime versions
are dated observations; dependency upgrades require fresh discovery and qualification.

| Decision | Status | Summary |
| --- | --- | --- |
| [Flux-native delivery](decisions/flux-native-delivery.md) | Accepted | Astronomer owns delivery intent, placement, rollout, and status while local Flux controllers converge each managed cluster. Rancher Fleet and Argo are not part of the v1 runtime. |
| [Frontend application stack](decisions/frontend-application-stack.md) | Accepted | The console uses REST/OpenAPI, generated wire contracts, TanStack Router/Query/Form, and the [latest stable tooling policy](decisions/latest-stable-build-tooling.md). |
| [Go platform tooling](decisions/go-platform-tooling.md) | Accepted | The backend standardizes on `slog`, goose (embedded), OTLP/gRPC, pinned standard lint, digest-pinned Alpine runtime images, and an explicit container rebuild workflow. |

## Conventions

- Use one Markdown file per decision under `docs/architecture/decisions`.
- State whether the decision is proposed, accepted, superseded, or rejected.
- Name authoritative stores and trust boundaries explicitly.
- Link machine-readable contracts instead of copying values that can drift.
- Add a superseding link to an older ADR rather than silently rewriting its
  historical context.
