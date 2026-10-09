# Reviewed Cosign JSON reader binding refresh

Source boundary `2a89bf5d` introduces two canonical reader sites in `internal/delivery/resolver/oci.go` and removes its permissive manifest decoder. The exact inventory records the bounded duplicate/depth/trailing rejection before existing pinned manifest and simple-signing claims validation. The second partial claims reader remains explicit to preserve optional vendor identity fields. No SQL site or SQL owner module is affected.

The prior inventory fails with exactly those two added sites and one removed site ([before](cosign-reader-before.log)). The updated exact inventory passes ([after](cosign-reader-after.log)); the actual QA-signed positive and ambiguous signed evidence fixture passes ([tests](cosign-reader-tests.log)). This metadata refresh preserves historical W09 qualification receipts and does not claim that their source hashes cover the later W03 implementation.
