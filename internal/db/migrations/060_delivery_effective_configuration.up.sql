ALTER TABLE public.delivery_targets
    ADD COLUMN configuration_template_id uuid REFERENCES public.delivery_configuration_templates(id) ON DELETE RESTRICT,
    ADD COLUMN override_set_ids uuid[] NOT NULL DEFAULT '{}'::uuid[];

ALTER TABLE public.delivery_targets
    ADD CONSTRAINT delivery_targets_override_set_limit
    CHECK (cardinality(override_set_ids) <= 64);

ALTER TABLE public.cluster_deployments
    ADD COLUMN desired_renderer_spec jsonb,
    ADD COLUMN desired_configuration_digest varchar(71);

ALTER TABLE public.cluster_deployments
    ADD CONSTRAINT cluster_deployments_configuration_digest
    CHECK (desired_configuration_digest IS NULL OR desired_configuration_digest ~ '^sha256:[0-9a-f]{64}$');

COMMENT ON COLUMN public.cluster_deployments.desired_renderer_spec IS
    'Frozen effective renderer spec for the desired rollout; null means use the immutable bundle version renderer.';
