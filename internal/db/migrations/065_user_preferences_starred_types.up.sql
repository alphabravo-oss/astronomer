ALTER TABLE public.user_preferences ADD COLUMN starred_types jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(starred_types) = 'array' AND jsonb_array_length(starred_types) <= 20);
