-- Align resources and collection with the product model:
-- nodes are shared across platforms inside one game; bid/ask are per
-- game × platform; summary targets carry appid; catalog is not a task.

DELETE FROM collection_pages
WHERE run_id IN (SELECT run_id FROM collection_runs WHERE kind = 'catalog');

DELETE FROM collection_runs WHERE kind = 'catalog';

DELETE FROM collection_targets WHERE kind = 'catalog';

DELETE FROM collection_pages
WHERE run_id IN (
    SELECT run_id FROM collection_runs
    WHERE target_id IN (SELECT target_id FROM collection_targets WHERE kind = 'summary')
);

DELETE FROM collection_runs
WHERE target_id IN (SELECT target_id FROM collection_targets WHERE kind = 'summary');

DELETE FROM collection_targets WHERE kind = 'summary';

DELETE FROM account_node_combinations;

ALTER TABLE account_node_combinations
    DROP CONSTRAINT account_node_combinations_node_platform_fkey;

ALTER TABLE access_nodes
    DROP CONSTRAINT access_nodes_node_platform_key;

ALTER TABLE access_nodes
    DROP CONSTRAINT access_nodes_assigned_platform_canonical;

ALTER TABLE access_nodes
    DROP COLUMN assigned_platform;

ALTER TABLE access_nodes
    ADD COLUMN appid BIGINT,
    ADD CONSTRAINT access_nodes_appid_positive CHECK (appid IS NULL OR appid > 0);

CREATE TABLE node_direction_assignments (
    node_id  BIGINT NOT NULL,
    platform TEXT COLLATE "C" NOT NULL,
    side     TEXT COLLATE "C" NOT NULL,
    CONSTRAINT node_direction_assignments_pkey PRIMARY KEY (node_id, platform),
    CONSTRAINT node_direction_assignments_node_fkey
        FOREIGN KEY (node_id) REFERENCES access_nodes (node_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT node_direction_assignments_platform_canonical
        CHECK (platform ~ '^[a-z][a-z0-9_.-]{0,31}$'),
    CONSTRAINT node_direction_assignments_side_valid CHECK (side IN ('bid', 'ask'))
);

ALTER TABLE account_node_combinations
    ADD CONSTRAINT account_node_combinations_node_direction_fkey
        FOREIGN KEY (node_id, platform) REFERENCES node_direction_assignments (node_id, platform)
        ON UPDATE RESTRICT ON DELETE RESTRICT;

ALTER TABLE collection_targets
    DROP CONSTRAINT collection_targets_kind_valid;

ALTER TABLE collection_targets
    ADD CONSTRAINT collection_targets_kind_valid CHECK (kind = 'summary');

ALTER TABLE collection_targets
    DROP CONSTRAINT collection_targets_kind_shape;

ALTER TABLE collection_targets
    ADD CONSTRAINT collection_targets_kind_shape CHECK (
        kind = 'summary' AND
        appid IS NOT NULL AND
        appid > 0 AND
        side IS NOT NULL AND
        side IN ('bid', 'ask') AND
        period_microseconds IS NULL
    );

DROP INDEX collection_targets_catalog_identity_key;

DROP INDEX collection_targets_summary_identity_key;

CREATE UNIQUE INDEX collection_targets_summary_identity_key
    ON collection_targets (platform, appid, side)
    WHERE kind = 'summary';

ALTER TABLE collection_runs
    DROP CONSTRAINT collection_runs_kind_valid;

ALTER TABLE collection_runs
    ADD CONSTRAINT collection_runs_kind_valid CHECK (kind IN ('summary', 'detail'));

ALTER TABLE collection_runs
    DROP CONSTRAINT collection_runs_kind_shape;

ALTER TABLE collection_runs
    ADD CONSTRAINT collection_runs_kind_shape CHECK (
        (
            kind = 'summary' AND
            target_id IS NOT NULL AND
            side IS NOT NULL AND
            side IN ('bid', 'ask') AND
            product_id IS NULL AND
            switch_version IS NOT NULL AND
            switch_version > 0
        ) OR
        (
            kind = 'detail' AND
            target_id IS NULL AND
            side IS NOT NULL AND
            side IN ('bid', 'ask') AND
            product_id IS NOT NULL AND
            product_id > 0 AND
            switch_version IS NULL
        )
    );

DROP INDEX collection_runs_active_catalog_key;

DROP INDEX collection_runs_active_summary_key;

CREATE UNIQUE INDEX collection_runs_active_summary_key
    ON collection_runs (target_id)
    WHERE kind = 'summary' AND status IN ('pending', 'running');
