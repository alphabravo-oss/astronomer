ALTER TABLE argocd_applications
    ADD COLUMN IF NOT EXISTS resource_created_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS resource_changed_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS resource_pruned_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE argocd_applications
    DROP CONSTRAINT IF EXISTS argocd_application_resource_counts_nonnegative;

ALTER TABLE argocd_applications
    ADD CONSTRAINT argocd_application_resource_counts_nonnegative
    CHECK (
        resource_created_count >= 0
        AND resource_changed_count >= 0
        AND resource_pruned_count >= 0
    );
