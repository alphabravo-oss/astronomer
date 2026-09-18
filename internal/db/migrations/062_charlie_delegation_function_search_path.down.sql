CREATE OR REPLACE FUNCTION public.revoke_charlie_delegations_for_deactivated_user() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    UPDATE charlie_delegations
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
    UPDATE charlie_delegations AS delegation
    SET revoked_at = now()
    FROM charlie_sessions AS session
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
    UPDATE charlie_delegations
    SET revoked_at = now()
    WHERE revoked_at IS NULL;
    RETURN NULL;
END;
$$;
