ALTER TABLE access_nodes
    ADD COLUMN assignment_revision BIGINT NOT NULL DEFAULT 1,
    ADD CONSTRAINT access_nodes_assignment_revision_positive CHECK (assignment_revision > 0);

CREATE TABLE account_node_combinations (
    combination_id BIGINT GENERATED ALWAYS AS IDENTITY,
    platform       TEXT COLLATE "C" NOT NULL,
    account_id     BIGINT NOT NULL,
    node_id        BIGINT NOT NULL,
    CONSTRAINT account_node_combinations_pkey PRIMARY KEY (combination_id),
    CONSTRAINT account_node_combinations_identity_positive CHECK (
        combination_id > 0 AND account_id > 0 AND node_id > 0
    ),
    CONSTRAINT account_node_combinations_platform_token CHECK (
        platform ~ '^[a-z][a-z0-9_.-]{0,31}$'
    ),
    CONSTRAINT account_node_combinations_account_node_key UNIQUE (account_id, node_id),
    CONSTRAINT account_node_combinations_account_platform_fkey
        FOREIGN KEY (account_id, platform)
        REFERENCES platform_accounts (account_id, platform)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT account_node_combinations_node_platform_fkey
        FOREIGN KEY (node_id, platform)
        REFERENCES access_nodes (node_id, assigned_platform)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE INDEX account_node_combinations_node_id_idx
    ON account_node_combinations (node_id);
