-- Serialize Dex lifecycle transitions in the query layer. Ciphertext-only
-- maintenance (keyrotate) intentionally bypasses logical runtime staging.
DROP TRIGGER IF EXISTS dex_connectors_runtime_generation ON dex_connectors;
DROP FUNCTION IF EXISTS bump_dex_runtime_generation();
