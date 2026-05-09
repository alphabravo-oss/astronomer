ALTER TABLE global_roles DROP COLUMN IF EXISTS display_name;
ALTER TABLE cluster_roles DROP COLUMN IF EXISTS display_name;
ALTER TABLE project_roles DROP COLUMN IF EXISTS display_name;
