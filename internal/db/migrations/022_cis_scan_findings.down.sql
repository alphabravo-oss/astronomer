-- Phase B5: rollback CIS scan additions.
DELETE FROM cluster_tools WHERE slug = 'cis-operator';

DROP INDEX IF EXISTS idx_security_scan_results_cluster_scan_name;

ALTER TABLE security_scan_results DROP COLUMN IF EXISTS findings;
ALTER TABLE security_scan_results DROP COLUMN IF EXISTS skipped;
ALTER TABLE security_scan_results DROP COLUMN IF EXISTS warned;
ALTER TABLE security_scan_results DROP COLUMN IF EXISTS failed;
ALTER TABLE security_scan_results DROP COLUMN IF EXISTS passed;
ALTER TABLE security_scan_results DROP COLUMN IF EXISTS cluster_scan_name;
