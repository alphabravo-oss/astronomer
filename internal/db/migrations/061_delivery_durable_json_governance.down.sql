DROP TRIGGER IF EXISTS durable_json_validate_write ON public.delivery_override_sets;
DROP TRIGGER IF EXISTS durable_json_validate_write ON public.delivery_configuration_templates;
DROP TRIGGER IF EXISTS durable_json_validate_write ON public.delivery_catalogs;
DROP TRIGGER IF EXISTS durable_json_validate_write ON public.catalog_blessed_charts;

DELETE FROM public.durable_json_schemas
WHERE table_schema = 'public'
  AND (table_name, column_name) IN (
      ('catalog_blessed_charts', 'artifact'),
      ('catalog_blessed_charts', 'compatibility'),
      ('catalog_blessed_charts', 'lifecycle'),
      ('catalog_blessed_charts', 'presentation'),
      ('catalog_blessed_charts', 'raw_entry'),
      ('catalog_blessed_charts', 'resources'),
      ('catalog_blessed_charts', 'storage'),
      ('cluster_deployments', 'desired_renderer_spec'),
      ('delivery_catalogs', 'trust_policy'),
      ('delivery_configuration_templates', 'patches'),
      ('delivery_configuration_templates', 'secret_refs'),
      ('delivery_configuration_templates', 'values_document'),
      ('delivery_controller_inventory', 'system_components'),
      ('delivery_override_sets', 'patches'),
      ('delivery_override_sets', 'values_document')
  );
