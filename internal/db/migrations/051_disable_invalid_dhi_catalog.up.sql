-- Docker Hardened Image charts are authenticated OCI artifacts published
-- through dhi.io. The historical seed points the HTTP Helm-index synchronizer
-- at a GitHub directory, which can never return index.yaml and therefore
-- creates a permanently retrying worker job. Disable only the untouched seed;
-- an operator-corrected row at the same stable ID is deliberately preserved.
UPDATE public.helm_repositories
SET enabled = false,
    last_sync_error = 'disabled invalid seed: Docker Hardened Image charts require an authenticated OCI catalog',
    updated_at = NOW()
WHERE id = '41f57bbb-f0c0-41ac-97c6-24051334943a'
  AND name = 'docker-hardened-images'
  AND url = 'https://github.com/docker-hardened-images/catalog/raw/main/chart'
  AND repo_type = 'helm'
  AND auth_type = 'none';
