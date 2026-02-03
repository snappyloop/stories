-- Add key_lookup column for secure API key lookup (sha256 hex of key)
-- key_hash continues to store bcrypt hash for verification
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_lookup TEXT UNIQUE;
CREATE UNIQUE INDEX IF NOT EXISTS idx_api_keys_key_lookup ON api_keys(key_lookup) WHERE key_lookup IS NOT NULL;
