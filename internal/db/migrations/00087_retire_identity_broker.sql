-- +goose Up
-- Operators must drain the old server/worker version before this cutover.
-- Keep product accounts, local MFA and all role bindings. Old connector subjects
-- cannot safely be inferred to be subjects at a replacement issuer.
-- Legacy access tokens do not carry the replacement issuer/subject binding.
-- Revoke existing sessions for broker-linked accounts before removing that
-- provenance. Their local credentials and roles survive; fresh login is required.
UPDATE public.users SET tokens_invalidated_at = GREATEST(tokens_invalidated_at, now())
WHERE id IN (SELECT user_id FROM public.external_principals
             UNION SELECT user_id FROM public.sso_sessions WHERE provider_name = 'dex');
UPDATE public.refresh_session_families
SET revoked_at = now(), revoke_reason = 'identity_broker_retired'
WHERE revoked_at IS NULL AND user_id IN (
    SELECT user_id FROM public.external_principals
    UNION SELECT user_id FROM public.sso_sessions WHERE provider_name = 'dex');
INSERT INTO public.jwt_revocations (jti, user_id, expires_at, reason)
SELECT jti, user_id, expires_at, 'identity_broker_retired'
FROM public.sso_sessions WHERE provider_name = 'dex'
ON CONFLICT (jti) DO NOTHING;
DELETE FROM public.task_outbox WHERE task_type IN ('dex:apply_operation', 'dex:recover_operations');
DELETE FROM public.sso_configurations WHERE provider = 'dex';
DELETE FROM public.sso_sessions WHERE provider_name = 'dex';
DELETE FROM public.cluster_tools WHERE slug = 'dex';
DROP TABLE public.external_principals;
DROP TABLE public.dex_operations;
DROP TABLE public.dex_connectors CASCADE;
DROP TABLE public.dex_settings;
DROP FUNCTION public.bump_dex_runtime_generation();
ALTER TABLE public.sso_configurations DROP COLUMN migrated_to_dex_at;
DELETE FROM public.durable_json_schemas WHERE table_name IN ('dex_connectors','dex_settings','dex_operations','external_principals');
