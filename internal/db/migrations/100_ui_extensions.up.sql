-- UI extension registry. This is the durable control plane for safe
-- extension install/enable/disable decisions; bundle execution stays disabled
-- until signed asset loading is implemented.

CREATE TABLE IF NOT EXISTS ui_extensions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 TEXT NOT NULL UNIQUE,
    display_name         TEXT NOT NULL,
    version              TEXT NOT NULL,
    source               TEXT NOT NULL DEFAULT 'manual',
    checksum             TEXT NOT NULL DEFAULT '',
    enabled              BOOLEAN NOT NULL DEFAULT false,
    compatibility_status TEXT NOT NULL DEFAULT 'unknown'
        CHECK (compatibility_status IN ('compatible', 'incompatible', 'unknown')),
    manifest             JSONB NOT NULL,
    installed_by         UUID REFERENCES users(id) ON DELETE SET NULL,
    installed_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_ui_extensions_enabled
    ON ui_extensions (enabled, compatibility_status);
