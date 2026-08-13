-- Drop encrypted credential columns and switch to plaintext storage.
-- Existing ciphertext cannot be recovered; clear and re-enter credentials.

DELETE FROM account_node_combinations;
DELETE FROM platform_accounts;
DELETE FROM access_nodes WHERE kind = 'proxy';

ALTER TABLE platform_accounts
    DROP COLUMN session_envelope_version,
    DROP COLUMN session_key_id,
    DROP COLUMN session_nonce,
    DROP COLUMN session_ciphertext;

ALTER TABLE platform_accounts
    ADD COLUMN session_plaintext BYTEA;

ALTER TABLE platform_accounts
    ALTER COLUMN session_plaintext SET NOT NULL,
    ADD CONSTRAINT platform_accounts_session_plaintext_nonempty
        CHECK (octet_length(session_plaintext) > 0);

ALTER TABLE access_nodes
    DROP COLUMN proxy_envelope_version,
    DROP COLUMN proxy_key_id,
    DROP COLUMN proxy_nonce,
    DROP COLUMN proxy_ciphertext;

ALTER TABLE access_nodes
    ADD COLUMN proxy_plaintext BYTEA,
    ADD CONSTRAINT access_nodes_proxy_plaintext_consistent CHECK (
        (
            kind = 'direct' AND
            proxy_plaintext IS NULL
        ) OR
        (
            kind = 'proxy' AND
            proxy_plaintext IS NOT NULL AND
            octet_length(proxy_plaintext) > 0
        )
    );
