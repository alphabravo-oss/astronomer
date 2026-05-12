-- Down-migration for 060_gitops_registration.
--
-- Drops gitops_registered_clusters first so its FK to
-- gitops_registration_sources doesn't dangle on rollback. Indexes go
-- away implicitly with their tables.

DROP TABLE IF EXISTS gitops_registered_clusters;
DROP TABLE IF EXISTS gitops_registration_sources;
