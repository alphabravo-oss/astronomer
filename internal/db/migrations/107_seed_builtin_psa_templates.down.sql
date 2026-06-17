-- Reverse sprint 107 built-in PSA starter template seeds.
--
-- We scope the delete to is_builtin rows so an operator who renamed a seeded
-- template (unlikely — they are edit-protected — but defensive) isn't matched
-- only by name. No cluster_security_policies rows were created by the up
-- migration, so there is nothing to detach here.

DELETE FROM pod_security_templates WHERE is_builtin = true;

ALTER TABLE pod_security_templates
    DROP COLUMN IF EXISTS is_builtin;
