ALTER TABLE public.delivery_controller_inventory
    ADD COLUMN system_components jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE public.delivery_controller_inventory
    ADD CONSTRAINT delivery_system_components_array
    CHECK (jsonb_typeof(system_components) = 'array');
