CREATE TABLE public.external_principals (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    connector_id uuid NOT NULL REFERENCES public.dex_connectors(id) ON DELETE RESTRICT,
    subject varchar(512) NOT NULL,
    email varchar(254) NOT NULL,
    username varchar(150) NOT NULL,
    display_name varchar(255) NOT NULL DEFAULT '',
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    linked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT external_principals_connector_subject_unique UNIQUE (connector_id, subject),
    CONSTRAINT external_principals_subject_not_blank CHECK (btrim(subject) <> ''),
    CONSTRAINT external_principals_email_canonical CHECK (email = lower(btrim(email)) AND email <> '')
);

CREATE INDEX idx_external_principals_user ON public.external_principals(user_id);
CREATE INDEX idx_external_principals_email ON public.external_principals(email);

CREATE TRIGGER set_updated_at
    BEFORE UPDATE ON public.external_principals
    FOR EACH ROW EXECUTE FUNCTION public.update_updated_at();
