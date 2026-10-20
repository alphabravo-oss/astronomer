# Architecture decision records

MADR-format decisions for the Astronomer realignment against the AlphaBravo
Technology Selection Guide. Earlier narrative decisions live under
`docs/architecture/decisions/` and remain valid where not superseded here.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](0001-tanstack-router.md) | TanStack Router v1 as the dashboard router | accepted |
| [0002](0002-tenancy-key.md) | Tenancy key is (cluster_id, project_id, namespace), not org_id | accepted |
| [0003](0003-redis-asynq-durable-queue.md) | Redis (Valkey-compatible) + asynq as the durable task queue | accepted |
| [0004](0004-cnpg-and-flux-delegated-delivery.md) | Preserve CNPG-managed PostgreSQL and Flux-delegated delivery | accepted |
| [0005](0005-hard-delete-with-audit-outbox.md) | Hard delete with transactional audit instead of deleted_at | accepted |
| [0006](0006-tailwind-v4-css-first.md) | Tailwind v4 CSS-first configuration | accepted |
| [0007](0007-rest-to-connect-rpc.md) | REST/OpenAPI to Connect-RPC migration strategy | proposed |
| [0008](0008-charlie-integration-retained.md) | Charlie integration is retained | accepted |
| [0009](0009-goose-embedded-migrations.md) | goose migrations embedded in the server, applied at boot | accepted |
| [0011](0011-zerolog-logging-with-slog-bridge.md) | zerolog via pkg/logging, with a log/slog bridge | accepted |
| [0012](0012-otel-meter-provider-prometheus-exporter.md) | pkg/telemetry: one Init for tracer and meter providers, one /metrics | accepted |
| [0015](0015-image-signature-admission.md) | Image signature admission with per-vendor authorities | accepted |

The threat model lives next to these decisions: [`../threat-model.md`](../threat-model.md).
