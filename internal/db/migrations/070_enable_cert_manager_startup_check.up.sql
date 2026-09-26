UPDATE public.cluster_tools
SET presets = jsonb_set(
        presets,
        '{default}',
        to_jsonb(replace(
            presets->>'default',
            E'startupapicheck:\n  enabled: false',
            E'startupapicheck:\n  enabled: true'
        )),
        false
    ),
    updated_at = NOW()
WHERE slug = 'cert-manager'
  AND presets->>'default' LIKE E'%startupapicheck:\n  enabled: false%';
