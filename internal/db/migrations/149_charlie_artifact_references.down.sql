ALTER TABLE charlie_connections
    DROP CONSTRAINT IF EXISTS charlie_connection_image_reference_pinned,
    DROP CONSTRAINT IF EXISTS charlie_connection_chart_reference_oci,
    DROP COLUMN IF EXISTS image_reference,
    DROP COLUMN IF EXISTS chart_reference;
