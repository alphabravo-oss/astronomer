-- Add a human-friendly display_name to RBAC role tables. The Next.js frontend
-- sends display_name on create; without this column the value was silently
-- dropped by the server, leaving roles with only their machine-friendly name.
ALTER TABLE global_roles
    ADD COLUMN display_name VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE cluster_roles
    ADD COLUMN display_name VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE project_roles
    ADD COLUMN display_name VARCHAR(255) NOT NULL DEFAULT '';
