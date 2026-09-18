-- Restore the exact historical seed only when it still carries the marker
-- written by the up migration. Operator edits remain authoritative.
UPDATE public.helm_repositories
SET enabled = true,
    last_sync_error = '',
    updated_at = NOW()
WHERE id = '41f57bbb-f0c0-41ac-97c6-24051334943a'
  AND name = 'docker-hardened-images'
  AND url = 'https://github.com/docker-hardened-images/catalog/raw/main/chart'
  AND repo_type = 'helm'
  AND auth_type = 'none'
  AND enabled = false
  AND last_sync_error = 'disabled invalid seed: Docker Hardened Image charts require an authenticated OCI catalog';
