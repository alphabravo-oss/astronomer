-- Populate immutable application history for Flux-owned installations that
-- predate migration 027. This is intentionally an insert-only projection;
-- source installation and Delivery identities remain authoritative.
INSERT INTO public.delivery_application_versions (
    installation_id, chart_version_id, bundle_version_id, catalog_slug,
    version, artifact_digest, values_digest, verification_status
)
SELECT installation.id, chart_version.id, target.bundle_version_id,
       catalog_entry.slug, chart_version.version,
       CASE WHEN chart_version.digest LIKE 'sha256:%' THEN chart_version.digest
            ELSE 'sha256:' || chart_version.digest END,
       'sha256:' || encode(digest(installation.values_override, 'sha256'), 'hex'),
       catalog_entry.verification_status
FROM public.installed_charts installation
JOIN public.helm_chart_versions chart_version
  ON chart_version.id=installation.chart_version_id
JOIN public.helm_charts chart ON chart.id=chart_version.chart_id
JOIN public.helm_repositories repository ON repository.id=chart.repository_id
JOIN public.catalog_blessed_charts catalog_entry
  ON catalog_entry.repo_url=repository.url
 AND catalog_entry.chart_name=chart.name
 AND catalog_entry.source='catalog-v1'
JOIN public.delivery_targets target ON target.id=installation.request_id
WHERE chart_version.digest ~ '^(sha256:)?[0-9a-f]{64}$'
ON CONFLICT (installation_id,chart_version_id,values_digest) DO NOTHING;
