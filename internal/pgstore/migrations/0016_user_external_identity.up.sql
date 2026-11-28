-- Give the existing SSO identity columns canonical names without dropping linked identities.
ALTER TABLE users RENAME COLUMN provider TO external_issuer;
ALTER TABLE users RENAME COLUMN external_id TO external_subject;
ALTER TABLE users RENAME CONSTRAINT users_external_pair TO users_external_identity_pair;
ALTER INDEX users_external_key RENAME TO users_external_identity_key;
