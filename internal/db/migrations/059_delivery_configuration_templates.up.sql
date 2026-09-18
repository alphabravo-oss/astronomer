CREATE TABLE public.delivery_configuration_templates (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES public.projects(id) ON DELETE CASCADE,
    name varchar(128) NOT NULL,
    description text NOT NULL DEFAULT '',
    renderer varchar(16) NOT NULL,
    values_document jsonb NOT NULL DEFAULT '{}'::jsonb,
    patches jsonb NOT NULL DEFAULT '[]'::jsonb,
    secret_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
    generation bigint NOT NULL DEFAULT 1,
    created_by uuid REFERENCES public.users(id) ON DELETE SET NULL,
    updated_by uuid REFERENCES public.users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT delivery_configuration_templates_renderer
        CHECK (renderer IN ('helm', 'kustomize')),
    CONSTRAINT delivery_configuration_templates_values_object
        CHECK (jsonb_typeof(values_document) = 'object'),
    CONSTRAINT delivery_configuration_templates_patches_array
        CHECK (jsonb_typeof(patches) = 'array' AND jsonb_array_length(patches) <= 64),
    CONSTRAINT delivery_configuration_templates_secret_refs_array
        CHECK (jsonb_typeof(secret_refs) = 'array' AND jsonb_array_length(secret_refs) <= 64),
    UNIQUE (project_id, name)
);

CREATE INDEX idx_delivery_configuration_templates_project_updated
    ON public.delivery_configuration_templates (project_id, updated_at DESC, id);

CREATE TABLE public.delivery_override_sets (
    id uuid DEFAULT gen_random_uuid() PRIMARY KEY,
    project_id uuid NOT NULL REFERENCES public.projects(id) ON DELETE CASCADE,
    template_id uuid REFERENCES public.delivery_configuration_templates(id) ON DELETE SET NULL,
    name varchar(128) NOT NULL,
    scope_type varchar(24) NOT NULL,
    scope_id uuid,
    precedence integer NOT NULL DEFAULT 0,
    values_document jsonb NOT NULL DEFAULT '{}'::jsonb,
    patches jsonb NOT NULL DEFAULT '[]'::jsonb,
    enabled boolean NOT NULL DEFAULT true,
    generation bigint NOT NULL DEFAULT 1,
    created_by uuid REFERENCES public.users(id) ON DELETE SET NULL,
    updated_by uuid REFERENCES public.users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT delivery_override_sets_scope
        CHECK (scope_type IN ('organization', 'project', 'environment', 'group', 'cluster', 'rollout')),
    CONSTRAINT delivery_override_sets_values_object
        CHECK (jsonb_typeof(values_document) = 'object'),
    CONSTRAINT delivery_override_sets_patches_array
        CHECK (jsonb_typeof(patches) = 'array' AND jsonb_array_length(patches) <= 64),
    UNIQUE (project_id, name)
);

CREATE INDEX idx_delivery_override_sets_precedence
    ON public.delivery_override_sets
       (project_id, enabled, precedence, scope_type, scope_id, name, id);

UPDATE public.global_roles
SET rules = rules::jsonb || '[{"resource":"delivery_configuration_templates","verbs":["create","read","update","delete","list"]}]'::jsonb
WHERE name = 'GitOps Admin';
UPDATE public.global_roles
SET rules = rules::jsonb || '[{"resource":"delivery_configuration_templates","verbs":["read","list"]}]'::jsonb
WHERE name IN ('GitOps Viewer', 'Auditor');
UPDATE public.project_roles
SET rules = rules::jsonb || '[{"resource":"delivery_configuration_templates","verbs":["create","read","update","delete","list"]}]'::jsonb
WHERE name = 'GitOps Deployer';
UPDATE public.project_roles
SET rules = rules::jsonb || '[{"resource":"delivery_configuration_templates","verbs":["read","list"]}]'::jsonb
WHERE name = 'Project Operator';
