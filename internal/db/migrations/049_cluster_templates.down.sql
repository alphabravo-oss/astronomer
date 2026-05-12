-- Reverse of 049_cluster_templates.up.sql. Drop order matters: the two
-- per-cluster tables FK into cluster_templates, so they must go first.

DROP TABLE IF EXISTS cluster_registration_policies;
DROP TABLE IF EXISTS cluster_template_applications;
DROP TABLE IF EXISTS cluster_templates;
