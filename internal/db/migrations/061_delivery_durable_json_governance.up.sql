-- Migrations 053-060 introduced delivery/catalog JSONB after the closed
-- inventory in migration 049 was created. Register every new durable column
-- before application startup so readiness can continue to prove that all JSON
-- writers have an explicit, versioned shape contract.
INSERT INTO public.durable_json_schemas (
    table_schema,
    table_name,
    column_name,
    schema_version,
    json_type,
    max_bytes,
    nullable,
    required_keys,
    compatibility_mode,
    owner
)
VALUES
    ('public', 'catalog_blessed_charts', 'artifact', 1, 'object', 16777216, false, '{}', 'additive', 'delivery'),
    ('public', 'catalog_blessed_charts', 'compatibility', 1, 'object', 16777216, false, '{}', 'additive', 'delivery'),
    ('public', 'catalog_blessed_charts', 'lifecycle', 1, 'object', 16777216, false, '{}', 'additive', 'delivery'),
    ('public', 'catalog_blessed_charts', 'presentation', 1, 'object', 16777216, false, '{}', 'additive', 'delivery'),
    ('public', 'catalog_blessed_charts', 'raw_entry', 1, 'object', 16777216, false, '{}', 'opaque', 'delivery'),
    ('public', 'catalog_blessed_charts', 'resources', 1, 'object', 16777216, false, '{}', 'additive', 'delivery'),
    ('public', 'catalog_blessed_charts', 'storage', 1, 'object', 16777216, false, '{}', 'additive', 'delivery'),
    ('public', 'cluster_deployments', 'desired_renderer_spec', 1, 'object', 16777216, true, '{}', 'additive', 'delivery'),
    ('public', 'delivery_catalogs', 'trust_policy', 1, 'object', 16777216, false, '{}', 'additive', 'delivery'),
    ('public', 'delivery_configuration_templates', 'patches', 1, 'array', 16777216, false, '{}', 'additive', 'delivery'),
    ('public', 'delivery_configuration_templates', 'secret_refs', 1, 'array', 16777216, false, '{}', 'additive', 'delivery'),
    ('public', 'delivery_configuration_templates', 'values_document', 1, 'object', 16777216, false, '{}', 'additive', 'delivery'),
    ('public', 'delivery_controller_inventory', 'system_components', 1, 'array', 16777216, false, '{}', 'additive', 'delivery'),
    ('public', 'delivery_override_sets', 'patches', 1, 'array', 16777216, false, '{}', 'additive', 'delivery'),
    ('public', 'delivery_override_sets', 'values_document', 1, 'object', 16777216, false, '{}', 'additive', 'delivery');

-- cluster_deployments and delivery_controller_inventory already carry the
-- migration-049 trigger. The four tables created later need their own writer
-- validation hook now that their contracts are registered.
CREATE TRIGGER durable_json_validate_write
    BEFORE INSERT OR UPDATE ON public.catalog_blessed_charts
    FOR EACH ROW EXECUTE FUNCTION public.validate_durable_jsonb_write();

CREATE TRIGGER durable_json_validate_write
    BEFORE INSERT OR UPDATE ON public.delivery_catalogs
    FOR EACH ROW EXECUTE FUNCTION public.validate_durable_jsonb_write();

CREATE TRIGGER durable_json_validate_write
    BEFORE INSERT OR UPDATE ON public.delivery_configuration_templates
    FOR EACH ROW EXECUTE FUNCTION public.validate_durable_jsonb_write();

CREATE TRIGGER durable_json_validate_write
    BEFORE INSERT OR UPDATE ON public.delivery_override_sets
    FOR EACH ROW EXECUTE FUNCTION public.validate_durable_jsonb_write();
