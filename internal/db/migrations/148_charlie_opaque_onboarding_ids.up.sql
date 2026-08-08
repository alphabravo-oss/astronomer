-- Charlie v1 identifiers are deliberately opaque and product identity is a
-- separate signed slug. This conditional upgrade also handles the development
-- deployment that applied migration 147 before the canonical contract was
-- exercised end to end. Fresh databases already have the final shape.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'charlie_connections'
          AND column_name = 'onboarding_package_id' AND udt_name = 'uuid'
    ) THEN
        ALTER TABLE charlie_connections
            ALTER COLUMN onboarding_package_id TYPE VARCHAR(128)
            USING onboarding_package_id::text;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'charlie_connections'
          AND column_name = 'product_slug'
    ) THEN
        ALTER TABLE charlie_connections ADD COLUMN product_slug VARCHAR(63);
        UPDATE charlie_connections SET product_slug = product_id;
        ALTER TABLE charlie_connections ALTER COLUMN product_slug SET NOT NULL;
    END IF;

    ALTER TABLE charlie_connections
        DROP CONSTRAINT IF EXISTS charlie_connection_product_astronomer;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'charlie_connection_product_slug_astronomer'
    ) THEN
        ALTER TABLE charlie_connections ADD CONSTRAINT charlie_connection_product_slug_astronomer
            CHECK (product_slug = 'astronomer');
    END IF;
END $$;
