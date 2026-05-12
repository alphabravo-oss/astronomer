-- Reverse of 075_seed_helm_repositories.up.sql.
--
-- Delete only the three seeded rows by name; never blow away repos an
-- operator has added on top. If an operator edited the URL/description
-- on one of the seeded rows, the down still removes it — there's no way
-- to tell an operator edit from a seeded row by content alone, and the
-- down's contract is "undo the up". Operators who customize seeded rows
-- and want to keep them across a downgrade should rename them.

DELETE FROM helm_repositories WHERE name IN ('bitnami', 'aqua', 'jetstack');
