-- Resize api_tokens.prefix from VARCHAR(8) to VARCHAR(16) so the
-- generated 12-char prefix written by the auth handler fits.
ALTER TABLE api_tokens ALTER COLUMN prefix TYPE VARCHAR(16);
