ALTER TABLE argocd_applications
    DROP CONSTRAINT IF EXISTS argocd_application_resource_counts_nonnegative;

ALTER TABLE argocd_applications
    DROP COLUMN IF EXISTS resource_pruned_count,
    DROP COLUMN IF EXISTS resource_changed_count,
    DROP COLUMN IF EXISTS resource_created_count;
