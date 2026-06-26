DROP INDEX IF EXISTS clusters_name_alive_unique;
CREATE UNIQUE INDEX clusters_name_key ON clusters (name);
