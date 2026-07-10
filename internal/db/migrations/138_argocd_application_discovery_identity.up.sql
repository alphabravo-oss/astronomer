ALTER TABLE argocd_applications
    ADD COLUMN IF NOT EXISTS upstream_uid VARCHAR(128) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS application_namespace VARCHAR(255) NOT NULL DEFAULT '';

COMMENT ON COLUMN argocd_applications.upstream_uid IS
    'Non-secret upstream Kubernetes UID used to bind durable operations to one Application incarnation.';
COMMENT ON COLUMN argocd_applications.application_namespace IS
    'Non-secret upstream Application object namespace; never stores Application spec or credentials.';
