CREATE TABLE public.user_preferences (
    user_id uuid PRIMARY KEY REFERENCES public.users(id) ON DELETE CASCADE,
    theme varchar(16) NOT NULL DEFAULT 'system'
        CHECK (theme IN ('light', 'dark', 'system')),
    table_density varchar(16) NOT NULL DEFAULT 'comfortable'
        CHECK (table_density IN ('compact', 'comfortable')),
    landing_route varchar(128) NOT NULL DEFAULT '/dashboard'
        CHECK (landing_route IN (
            '/dashboard', '/dashboard/clusters', '/dashboard/projects',
            '/dashboard/workloads', '/dashboard/delivery',
            '/dashboard/monitoring', '/dashboard/alerting',
            '/dashboard/security', '/dashboard/audit'
        )),
    time_format varchar(16) NOT NULL DEFAULT 'locale'
        CHECK (time_format IN ('locale', '12h', '24h')),
    favorites jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(favorites) = 'array' AND jsonb_array_length(favorites) <= 12),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE public.user_preferences IS
    'Typed, server-owned operator console preferences; one complete document per user.';
