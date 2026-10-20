-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.user_preferences ADD COLUMN pinned_clusters jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(pinned_clusters) = 'array' AND jsonb_array_length(pinned_clusters) <= 20);
-- +goose StatementEnd
