ALTER TABLE public.clusters
    ADD COLUMN badge_text varchar(24) NOT NULL DEFAULT '',
    ADD COLUMN badge_color varchar(16) NOT NULL DEFAULT '',
    ADD CONSTRAINT clusters_badge_color_valid CHECK (
        badge_color IN ('', 'slate', 'blue', 'green', 'amber', 'red', 'purple')
    ),
    ADD CONSTRAINT clusters_badge_pair_valid CHECK (
        (badge_text = '' AND badge_color = '')
        OR (badge_text <> '' AND badge_color <> '')
    );
