-- Downgrade only after reviewed exact database/table/index/OID/source ownership
-- and clean 81 checks. No IF EXISTS or CASCADE masks a collision or dependency.
-- One standalone statement; no transaction block or appended statements.
DROP INDEX CONCURRENTLY public.idx_catalog_global_chart_keys_ordered;
