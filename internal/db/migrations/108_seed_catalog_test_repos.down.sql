-- Remove the migration-108 seeded repos. Charts/versions cascade via FK.
DELETE FROM helm_repositories
WHERE name IN ('jetstack', 'kyverno', 'external-secrets', 'longhorn')
  AND is_default = true;
