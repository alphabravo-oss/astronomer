UPDATE public.read_audit_policies
SET path_pattern = '/support-bundle', updated_at = now()
WHERE name = 'support_bundle_download';

DROP TABLE IF EXISTS public.support_bundle_operations;
