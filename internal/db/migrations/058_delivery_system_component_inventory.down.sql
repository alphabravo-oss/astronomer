ALTER TABLE public.delivery_controller_inventory
    DROP CONSTRAINT IF EXISTS delivery_system_components_array,
    DROP COLUMN IF EXISTS system_components;
