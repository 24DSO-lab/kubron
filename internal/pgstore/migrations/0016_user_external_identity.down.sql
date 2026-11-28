DROP INDEX IF EXISTS users_external_identity_key;
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_external_identity_pair,
    DROP COLUMN IF EXISTS external_issuer,
    DROP COLUMN IF EXISTS external_subject;
