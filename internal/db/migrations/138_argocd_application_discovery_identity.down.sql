ALTER TABLE argocd_applications
    DROP COLUMN IF EXISTS application_namespace,
    DROP COLUMN IF EXISTS upstream_uid;
