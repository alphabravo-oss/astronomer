-- +goose Up
-- +goose StatementBegin
-- Legacy CLI credentials must not live forever. Expired credentials are
-- excluded from quota by the runtime queries; revocation remains explicit.
UPDATE public.api_tokens SET expires_at = created_at + interval '30 days'
WHERE name LIKE 'astro-cli%' AND expires_at IS NULL;
-- +goose StatementEnd
