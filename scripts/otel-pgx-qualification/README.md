# Owned pgx OTLP callback qualification

This helper requires a 0600 regular, non-symlink JSON reference with `database_url` identifying exactly `plan032_w10_callbacks`. Only standard TLS/timeout/application-name query options are accepted. It verifies the database again before creating/truncating its own `callback_rows` fixture table. Never point it at a shared/source/recovery database.

```sh
go build -o /private/cache/otel-pgx-qualification ./scripts/otel-pgx-qualification
/private/cache/otel-pgx-qualification --database-ref /private/cache/database.json --out /private/cache/new-receipt.json
```

The actual production `db.ConnectWithConfig` tracer closure exercises pool acquisition, an explicit prepared query, and SendBatch. A separate connection uses the unchanged production ConnConfig to characterize Connect without assuming every background pool connection inherits a sampled request. A real loopback OTLP/HTTP protobuf receiver checks callback spans, two committed rows, one root trace and absence of the bound synthetic sensitive value and actual DSN/password. Batch-query spans must be children of the actual batch span.

The output is a content-free sanitized receipt, created exclusively. Connection failures never print credentials. This proves this callback closure on the named owned database, not API/queue/tunnel execution, live collector migration, HA, throughput acceptance or broad redaction.
