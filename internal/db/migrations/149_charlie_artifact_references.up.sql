ALTER TABLE charlie_connections
    ADD COLUMN chart_reference VARCHAR(512),
    ADD COLUMN image_reference VARCHAR(512);

UPDATE charlie_connections
SET chart_reference = regexp_replace(central_url, '^https://', 'oci://') || '/charlie/agent-chart',
    image_reference = regexp_replace(central_url, '^https://', '') || '/charlie/agent@' || image_digest;

ALTER TABLE charlie_connections
    ALTER COLUMN chart_reference SET NOT NULL,
    ALTER COLUMN image_reference SET NOT NULL,
    ADD CONSTRAINT charlie_connection_chart_reference_oci
        CHECK (chart_reference ~ '^oci://[^/]+/.+'),
    ADD CONSTRAINT charlie_connection_image_reference_pinned
        CHECK (image_reference ~ '@sha256:[0-9a-f]{64}$');
