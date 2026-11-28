-- Store canonical issuer and subject separately from legacy provider/external_id fields.
ALTER TABLE users
    ADD COLUMN external_issuer text,
    ADD COLUMN external_subject text,
    ADD CONSTRAINT users_external_identity_pair
        CHECK ((external_issuer IS NULL) = (external_subject IS NULL));

CREATE UNIQUE INDEX users_external_identity_key
    ON users (org_id, external_issuer, external_subject)
    WHERE external_subject IS NOT NULL;
