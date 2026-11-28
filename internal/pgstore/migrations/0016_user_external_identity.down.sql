ALTER INDEX users_external_identity_key RENAME TO users_external_key;
ALTER TABLE users RENAME CONSTRAINT users_external_identity_pair TO users_external_pair;
ALTER TABLE users RENAME COLUMN external_subject TO external_id;
ALTER TABLE users RENAME COLUMN external_issuer TO provider;
