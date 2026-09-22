ALTER TABLE public.user_preferences ADD COLUMN rows_per_page integer NOT NULL DEFAULT 25 CHECK (rows_per_page IN (10, 25, 50, 100));
ALTER TABLE public.user_preferences ADD COLUMN date_format varchar(16) NOT NULL DEFAULT 'locale' CHECK (date_format IN ('locale', 'iso', 'relative'));
