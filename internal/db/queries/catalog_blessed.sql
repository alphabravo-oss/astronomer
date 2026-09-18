-- Blessed-chart catalog overlays (sourced from astronomer-catalog/catalog.yaml).

-- name: UpsertDefaultHelmRepository :exec
-- Seed/refresh a platform-default repo. On conflict we update the URL/description
-- and re-assert is_default, but deliberately leave `enabled` alone so an operator
-- who disabled a default repo keeps it disabled across reconciles.
INSERT INTO helm_repositories (name, url, repo_type, description, is_default, enabled)
VALUES ($1, $2, 'helm', $3, true, true)
ON CONFLICT (name) DO UPDATE SET
    url = EXCLUDED.url,
    description = EXCLUDED.description,
    is_default = true,
    updated_at = now();

-- name: DeleteBlessedChartsBySource :exec
DELETE FROM catalog_blessed_charts WHERE source = $1;

-- name: CreateBlessedChart :exec
INSERT INTO catalog_blessed_charts
    (repo_url, chart_name, display_name, description, category, icon_url, mgmt_safe, version_policy, source)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: ReconcileApplicationCatalogV1 :one
-- A single statement owns the complete catalog snapshot: malformed input is
-- rejected in Go before this runs, and any SQL failure rolls back catalog,
-- repository, entry upserts, and stale-entry deletion together. This gives us
-- last-known-good behavior without an application-managed transaction.
WITH input AS (
    SELECT sqlc.arg(document)::jsonb AS doc,
           sqlc.arg(source_url)::text AS source_url,
           sqlc.arg(source_revision)::text AS source_revision,
           sqlc.arg(index_digest)::text AS index_digest,
           sqlc.arg(verification_status)::text AS verification_status,
           sqlc.arg(verification_identity)::text AS verification_identity
), catalog_upsert AS (
    INSERT INTO delivery_catalogs (
        name, display_name, description, channel, source_url, source_revision,
        index_digest, verification_status, verification_identity, trust_policy,
        last_sync_attempted_at, last_synced_at, last_sync_error
    )
    SELECT doc->'metadata'->>'name',
           COALESCE(doc->'metadata'->>'displayName', doc->'metadata'->>'name'),
           COALESCE(doc->'metadata'->>'description', ''),
           COALESCE(doc->'metadata'->>'channel', 'stable'), source_url,
           source_revision, index_digest, verification_status,
           verification_identity,
           jsonb_build_object('immutableSource', source_revision <> '', 'transport', 'https'),
           now(), now(), ''
    FROM input
    ON CONFLICT (name) DO UPDATE SET
        display_name=EXCLUDED.display_name, description=EXCLUDED.description,
        channel=EXCLUDED.channel, source_url=EXCLUDED.source_url,
        source_revision=EXCLUDED.source_revision,
        index_digest=EXCLUDED.index_digest,
        verification_status=EXCLUDED.verification_status,
        verification_identity=EXCLUDED.verification_identity,
        trust_policy=EXCLUDED.trust_policy,
        last_sync_attempted_at=now(), last_synced_at=now(),
        last_sync_error='', updated_at=now()
    RETURNING name
), repo_rows AS (
    SELECT repository FROM input,
         LATERAL jsonb_array_elements(doc->'repositories') AS repository
), repo_upsert AS (
    INSERT INTO helm_repositories (name,url,repo_type,description,is_default,enabled)
    SELECT repository->>'name', repository->>'url',
           COALESCE(repository->>'type','helm'),
           'Curated by the Astronomer application catalog.', true, true
    FROM repo_rows
    ON CONFLICT (name) DO UPDATE SET
        url=EXCLUDED.url, repo_type=EXCLUDED.repo_type,
        description=EXCLUDED.description, is_default=true, updated_at=now()
    RETURNING name
), app_rows AS (
    SELECT application,
           repository->>'url' AS repo_url,
           repository->>'name' AS repo_name,
           input.index_digest,
           input.verification_status,
           input.verification_identity
    FROM input
    CROSS JOIN LATERAL jsonb_array_elements(doc->'applications') AS application
    JOIN LATERAL jsonb_array_elements(doc->'repositories') AS repository
      ON repository->>'name'=application->'artifact'->>'repository'
), entry_upsert AS (
    INSERT INTO catalog_blessed_charts (
        repo_url, chart_name, display_name, description, category, icon_url,
        mgmt_safe, version_policy, source, slug, repo_name, support_tier,
        featured, privileged, default_enabled, documentation_url, presentation,
        artifact, compatibility, resources, storage, lifecycle, raw_entry,
        catalog_digest, verification_status, verification_identity, revoked
    )
    SELECT repo_url, application->'artifact'->>'chart', application->>'name',
           COALESCE(application->>'description', application->>'summary', ''),
           COALESCE(application->>'category','other'), COALESCE(application->>'icon',''),
           NOT COALESCE((application->>'privileged')::boolean,false),
           CASE WHEN application->'artifact'->>'version' IS NULL THEN ''
                ELSE 'pinned:' || (application->'artifact'->>'version') END,
           'catalog-v1', application->>'slug', repo_name,
           COALESCE(application->>'supportTier','upstream'),
           COALESCE((application->'ui'->>'featured')::boolean,false),
           COALESCE((application->>'privileged')::boolean,false),
           COALESCE((application->>'defaultEnabled')::boolean,false),
           COALESCE(application->>'documentation',''),
           COALESCE(application->'ui','{}'::jsonb),
           COALESCE(application->'artifact','{}'::jsonb),
           COALESCE(application->'compatibility','{}'::jsonb),
           COALESCE(application->'resources','{}'::jsonb),
           COALESCE(application->'storage','{}'::jsonb),
           COALESCE(application->'lifecycle','{}'::jsonb), application,
           index_digest, verification_status, verification_identity, false
    FROM app_rows
    ON CONFLICT (repo_url,chart_name) DO UPDATE SET
        display_name=EXCLUDED.display_name, description=EXCLUDED.description,
        category=EXCLUDED.category, icon_url=EXCLUDED.icon_url,
        mgmt_safe=EXCLUDED.mgmt_safe, version_policy=EXCLUDED.version_policy,
        source=EXCLUDED.source, slug=EXCLUDED.slug, repo_name=EXCLUDED.repo_name,
        support_tier=EXCLUDED.support_tier, featured=EXCLUDED.featured,
        privileged=EXCLUDED.privileged, default_enabled=EXCLUDED.default_enabled,
        documentation_url=EXCLUDED.documentation_url,
        presentation=EXCLUDED.presentation, artifact=EXCLUDED.artifact,
        compatibility=EXCLUDED.compatibility, resources=EXCLUDED.resources,
        storage=EXCLUDED.storage, lifecycle=EXCLUDED.lifecycle,
        raw_entry=EXCLUDED.raw_entry, catalog_digest=EXCLUDED.catalog_digest,
        verification_status=EXCLUDED.verification_status,
        verification_identity=EXCLUDED.verification_identity,
        revoked=false, updated_at=now()
    RETURNING slug
), stale_delete AS (
    DELETE FROM catalog_blessed_charts existing
    WHERE existing.source='catalog-v1'
      AND NOT EXISTS (SELECT 1 FROM app_rows WHERE application->>'slug'=existing.slug)
    RETURNING id
)
SELECT count(*)::bigint AS entry_count FROM entry_upsert;

-- name: ListApplicationCatalogPresentations :many
SELECT id, slug, repo_name, repo_url, chart_name, display_name, description,
       category, icon_url, support_tier, featured, privileged, default_enabled,
       documentation_url, presentation, artifact, compatibility, resources,
       storage, lifecycle, catalog_digest, verification_status,
       verification_identity, revoked, updated_at
FROM catalog_blessed_charts
WHERE source='catalog-v1'
ORDER BY featured DESC, display_name ASC;

-- name: ListApplicationCatalogSources :many
SELECT id, name, display_name, description, channel, source_url,
       source_revision, index_digest, verification_status,
       verification_identity, trust_policy, last_sync_attempted_at,
       last_synced_at, last_sync_error, created_at, updated_at
FROM delivery_catalogs
ORDER BY display_name ASC, name ASC;

-- name: RecordApplicationCatalogSyncFailure :exec
UPDATE delivery_catalogs
SET last_sync_attempted_at=now(),
    last_sync_error=left(sqlc.arg(sync_error)::text, 2000),
    updated_at=now()
WHERE source_url=sqlc.arg(source_url);

-- name: GetApplicationCatalogPresentationByChartVersion :one
SELECT b.*
FROM helm_chart_versions v
JOIN helm_charts c ON c.id=v.chart_id
JOIN helm_repositories r ON r.id=c.repository_id
JOIN catalog_blessed_charts b
  ON b.repo_url=r.url AND b.chart_name=c.name AND b.source='catalog-v1'
WHERE v.id=$1;
