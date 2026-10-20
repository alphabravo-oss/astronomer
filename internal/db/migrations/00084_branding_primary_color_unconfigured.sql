-- +goose Up
-- +goose StatementBegin
-- branding.primary_color was seeded with the product default '#0066CC', so the
-- UI could not tell an operator's choice from the default. The registry default
-- is now "" (unconfigured); reset the seeded row only when nobody changed it.
UPDATE public.platform_settings
SET value = '""'::jsonb,
    description = 'Primary brand color (hex); empty means not configured (the UI uses its theme default)'
WHERE key = 'branding.primary_color'
  AND value = '"#0066CC"'::jsonb
  AND updated_by IS NULL;
-- +goose StatementEnd
