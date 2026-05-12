-- Migration 066 — cluster groups (folders / label-style organization).
--
-- Operators running tens of clusters want hierarchical org structure:
-- group by environment, region, business unit. Sprint 066 introduces a
-- self-FK tree (max depth 3 — enforced at the handler layer) plus a
-- direct group_id column on clusters so the common "what group am I in?"
-- lookup is a single indexed read.
--
-- Schema notes:
--   - parent_id NULL == top-level group.
--   - ON DELETE CASCADE on the self-FK so deleting a subtree is one call;
--     clusters whose group sits inside the cascaded subtree get
--     group_id=NULL (per ON DELETE SET NULL below).
--   - (parent_id, slug) UNIQUE: same parent can't have two children with
--     the same slug, but two distinct parents may each own a `prod-east`.
--     The (NULL, slug) row for top-level groups participates in this
--     constraint because Postgres treats UNIQUE with NULLs as "NULLs
--     distinct" by default — we DO want top-level slugs to be globally
--     unique, so we add a separate partial index on slug WHERE
--     parent_id IS NULL.
--   - enabled is a soft-delete flag so historical audit rows can still
--     resolve group names cheaply by JOIN.

CREATE TABLE cluster_groups (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(128) NOT NULL,
    slug            VARCHAR(128) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    parent_id       UUID REFERENCES cluster_groups(id) ON DELETE CASCADE,
    color           VARCHAR(16) NOT NULL DEFAULT '#6b7280',
    icon            VARCHAR(64) NOT NULL DEFAULT 'folder',
    enabled         BOOLEAN NOT NULL DEFAULT true,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (parent_id, slug)
);

-- Fast "list children of group X" — the recursive CTE in ListGroupsAsTree
-- walks this index repeatedly. WHERE enabled = true skips soft-deleted
-- rows because the tree view never wants to render them.
CREATE INDEX idx_cluster_groups_parent ON cluster_groups (parent_id) WHERE enabled = true;

-- Top-level slug uniqueness: the (parent_id, slug) UNIQUE above doesn't
-- catch duplicates among top-level groups because Postgres treats NULL
-- as distinct in UNIQUE indexes by default. This partial unique index
-- closes that gap.
CREATE UNIQUE INDEX idx_cluster_groups_toplevel_slug ON cluster_groups (slug) WHERE parent_id IS NULL;

-- Each cluster belongs to AT MOST one group. group_id is a column on
-- clusters (not a join table) so the per-cluster lookup is a single
-- indexed read instead of a join, which matters because nearly every
-- cluster GET surfaces the group breadcrumb. ON DELETE SET NULL keeps
-- the cluster alive when its group is dropped — the cluster itself is
-- never the thing being deleted via this cascade.
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS group_id UUID
    REFERENCES cluster_groups(id) ON DELETE SET NULL;

-- Partial index — the vast majority of clusters will have NULL group_id
-- until an operator opts in, so a full index would be mostly empty
-- entries.
CREATE INDEX IF NOT EXISTS idx_clusters_group ON clusters (group_id) WHERE group_id IS NOT NULL;
