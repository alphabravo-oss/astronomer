-- Removing this additive index does not alter row data or the schema floor.
DROP INDEX CONCURRENTLY IF EXISTS public.idx_helm_charts_keyset;
