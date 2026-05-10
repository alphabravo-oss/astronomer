ALTER TABLE platform_configuration
ADD COLUMN IF NOT EXISTS instance_id UUID NOT NULL DEFAULT gen_random_uuid();
