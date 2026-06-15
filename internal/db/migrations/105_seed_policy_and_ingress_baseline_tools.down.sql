-- Reverse sprint 105 baseline catalog seeds.
--
-- These rows are seed data only. Operators who changed them after install can
-- re-add their private mirror rows after downgrade if needed.

DELETE FROM cluster_tools WHERE slug IN (
    'ingress-nginx',
    'gatekeeper'
);

DELETE FROM helm_repositories WHERE name IN (
    'ingress-nginx',
    'open-policy-agent'
);
