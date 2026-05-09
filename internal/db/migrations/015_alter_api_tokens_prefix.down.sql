-- Revert api_tokens.prefix back to VARCHAR(8). Note: longer prefixes
-- already written would be truncated; this is intentionally a logical revert.
ALTER TABLE api_tokens ALTER COLUMN prefix TYPE VARCHAR(8);
