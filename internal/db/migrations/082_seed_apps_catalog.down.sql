-- Remove the Apps-tab seed entries. Only removes rows that still match
-- the seeded URLs — operators who pointed these names at a private
-- mirror keep their customisations.

DELETE FROM helm_repositories
WHERE name = 'prometheus-community'
  AND url  = 'https://prometheus-community.github.io/helm-charts';

DELETE FROM helm_repositories
WHERE name = 'grafana'
  AND url  = 'https://grafana.github.io/helm-charts';
