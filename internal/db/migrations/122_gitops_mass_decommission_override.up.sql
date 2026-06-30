-- Task E3 (H10): per-source one-shot override for the mass-decommission
-- guard. When the sync worker refuses a mass removal under
-- on_delete='decommission' (parsed_docs empty OR missing-count over the
-- safety threshold), an operator who genuinely intends the removal arms
-- this flag via the source PUT endpoint. The worker honors it exactly
-- once and self-disarms (ConsumeGitOpsMassDecommissionOverride) so a
-- leftover arm cannot permit a SECOND accidental bad sync.
ALTER TABLE gitops_registration_sources ADD COLUMN allow_mass_decommission BOOLEAN NOT NULL DEFAULT false;
