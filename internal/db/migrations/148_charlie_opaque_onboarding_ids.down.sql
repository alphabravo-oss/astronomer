-- The canonical opaque Charlie identifiers cannot be safely converted to UUID.
-- Rollback removes only the separate product slug added for an upgraded 147
-- database; migration 147 down remains the supported full Charlie rollback.
ALTER TABLE charlie_connections DROP CONSTRAINT IF EXISTS charlie_connection_product_slug_astronomer;
