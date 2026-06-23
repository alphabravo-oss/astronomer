-- P1 ns-scoped-rbac: persist the namespace scope on cluster role bindings.
-- Empty string ('') means the binding applies to the full cluster scope, matching
-- the in-memory rbac.RoleBinding.Namespace semantics (empty == cluster-wide).
ALTER TABLE cluster_role_bindings ADD COLUMN namespace VARCHAR(253) NOT NULL DEFAULT '';
