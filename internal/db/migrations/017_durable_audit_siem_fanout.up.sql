-- Match the small fnmatch dialect accepted by SIEM/webhook event filters:
-- '*' consumes any run of characters and '?' consumes exactly one. Event
-- names and configured patterns are bounded ASCII identifiers, so byte-wise
-- substring operations are intentional and deterministic.
CREATE FUNCTION public.astronomer_event_glob_match(pattern text, event_name text)
RETURNS boolean
LANGUAGE plpgsql
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
DECLARE
    pattern_pos integer := 1;
    event_pos integer := 1;
    star_pos integer := 0;
    retry_event_pos integer := 0;
    pattern_len integer := length(pattern);
    event_len integer := length(event_name);
    token text;
BEGIN
    WHILE event_pos <= event_len LOOP
        token := CASE
            WHEN pattern_pos <= pattern_len THEN substr(pattern, pattern_pos, 1)
            ELSE ''
        END;
        IF token = '?' OR token = substr(event_name, event_pos, 1) THEN
            pattern_pos := pattern_pos + 1;
            event_pos := event_pos + 1;
        ELSIF token = '*' THEN
            star_pos := pattern_pos;
            pattern_pos := pattern_pos + 1;
            retry_event_pos := event_pos;
        ELSIF star_pos > 0 THEN
            pattern_pos := star_pos + 1;
            retry_event_pos := retry_event_pos + 1;
            event_pos := retry_event_pos;
        ELSE
            RETURN false;
        END IF;
    END LOOP;

    WHILE pattern_pos <= pattern_len AND substr(pattern, pattern_pos, 1) = '*' LOOP
        pattern_pos := pattern_pos + 1;
    END LOOP;
    RETURN pattern_pos > pattern_len;
END;
$$;

COMMENT ON FUNCTION public.astronomer_event_glob_match(text, text) IS
    'Immutable fnmatch helper used by transactional audit-to-SIEM fan-out.';
