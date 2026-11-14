-- +goose Up
-- External authentication identifies an existing product account only through
-- an explicitly approved issuer/subject binding. Email is never a linking key.
CREATE TABLE public.shared_identity_bindings (
    issuer text NOT NULL CHECK (length(issuer) BETWEEN 1 AND 2048),
    subject text NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    linked_by uuid NOT NULL REFERENCES public.users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (issuer, subject)
);
CREATE INDEX shared_identity_bindings_user_idx ON public.shared_identity_bindings(user_id);
