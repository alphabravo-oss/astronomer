-- Sprint 077 down — remove the seeded prometheus-community repo only.
-- Operator-added repos pointing at the same URL under a different name
-- are untouched.

DELETE FROM helm_repositories WHERE name = 'prometheus-community';
