DROP TABLE IF EXISTS public.delivery_override_sets;
DROP TABLE IF EXISTS public.delivery_configuration_templates;

UPDATE public.global_roles
SET rules = COALESCE((SELECT jsonb_agg(rule) FROM jsonb_array_elements(rules::jsonb) rule WHERE rule->>'resource' <> 'delivery_configuration_templates'), '[]'::jsonb)
WHERE name IN ('GitOps Admin', 'GitOps Viewer', 'Auditor');
UPDATE public.project_roles
SET rules = COALESCE((SELECT jsonb_agg(rule) FROM jsonb_array_elements(rules::jsonb) rule WHERE rule->>'resource' <> 'delivery_configuration_templates'), '[]'::jsonb)
WHERE name IN ('GitOps Deployer', 'Project Operator');
