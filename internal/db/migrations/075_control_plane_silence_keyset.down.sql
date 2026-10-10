-- Remove only this additive ordering index; no data rollback.
DROP INDEX CONCURRENTLY IF EXISTS public.idx_control_plane_silences_keyset;
