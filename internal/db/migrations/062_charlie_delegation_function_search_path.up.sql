-- Keep security-sensitive revocation triggers independent of the caller's
-- search_path. The canonical schema deliberately clears search_path during
-- migration, and administrative role changes must remain fail-closed there.
CREATE OR REPLACE FUNCTION public.revoke_charlie_delegations_for_deactivated_user() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    UPDATE public.charlie_delegations
    SET revoked_at = now()
    WHERE principal_id = NEW.id
      AND revoked_at IS NULL;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.revoke_charlie_delegations_for_inactive_connection() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    UPDATE public.charlie_delegations AS delegation
    SET revoked_at = now()
    FROM public.charlie_sessions AS session
    WHERE session.connection_id = NEW.id
      AND delegation.session_id = session.id
      AND delegation.revoked_at IS NULL;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.revoke_charlie_delegations_on_rbac_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    UPDATE public.charlie_delegations
    SET revoked_at = now()
    WHERE revoked_at IS NULL;
    RETURN NULL;
END;
$$;
