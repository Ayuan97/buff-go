CREATE TABLE collection_targets (
    target_id                  BIGINT GENERATED ALWAYS AS IDENTITY,
    kind                       TEXT COLLATE "C" NOT NULL,
    platform                   TEXT COLLATE "C" NOT NULL,
    appid                      BIGINT,
    side                       TEXT COLLATE "C",
    desired_state              TEXT COLLATE "C" NOT NULL,
    actual_state               TEXT COLLATE "C" NOT NULL,
    reason_code                TEXT COLLATE "C" NOT NULL DEFAULT '',
    recovery_mode              TEXT COLLATE "C" NOT NULL DEFAULT '',
    next_check_at              TIMESTAMPTZ,
    period_microseconds        BIGINT,
    revision                   BIGINT NOT NULL DEFAULT 1,
    switch_version             BIGINT NOT NULL DEFAULT 1,
    next_run_sequence          BIGINT NOT NULL DEFAULT 1,
    changed_at                 TIMESTAMPTZ NOT NULL,
    CONSTRAINT collection_targets_pkey PRIMARY KEY (target_id),
    CONSTRAINT collection_targets_identity_positive CHECK (target_id > 0),
    CONSTRAINT collection_targets_kind_valid CHECK (kind IN ('catalog', 'summary')),
    CONSTRAINT collection_targets_platform_canonical CHECK (
        platform ~ '^[a-z][a-z0-9_.-]{0,31}$'
    ),
    CONSTRAINT collection_targets_side_valid CHECK (side IS NULL OR side IN ('bid', 'ask')),
    CONSTRAINT collection_targets_kind_shape CHECK (
        (
            kind = 'catalog' AND
            platform = 'steam' AND
            appid IS NOT NULL AND
            appid > 0 AND
            side IS NULL AND
            period_microseconds IS NOT NULL AND
            period_microseconds > 0 AND
            period_microseconds <= 9223372036854775
        ) OR
        (
            kind = 'summary' AND
            appid IS NULL AND
            side IS NOT NULL AND
            side IN ('bid', 'ask') AND
            period_microseconds IS NULL
        )
    ),
    CONSTRAINT collection_targets_desired_state_valid CHECK (
        desired_state IN ('enabled', 'disabled')
    ),
    CONSTRAINT collection_targets_actual_state_valid CHECK (
        actual_state IN ('starting', 'waiting', 'running', 'blocked', 'stopping', 'stopped', 'error')
    ),
    CONSTRAINT collection_targets_desired_actual_shape CHECK (
        (
            desired_state = 'enabled' AND
            actual_state IN ('starting', 'waiting', 'running', 'blocked', 'error')
        ) OR
        (
            desired_state = 'disabled' AND
            actual_state IN ('stopping', 'stopped')
        )
    ),
    CONSTRAINT collection_targets_recovery_mode_valid CHECK (
        recovery_mode IN ('', 'automatic', 'manual')
    ),
    CONSTRAINT collection_targets_reason_valid CHECK (
        reason_code = '' OR reason_code IN (
            'next_cycle',
            'scheduler_opportunity',
            'transient_failure',
            'no_combination',
            'cooldown',
            'egress_unavailable',
            'missing_rate_policy',
            'session_invalid',
            'invalid_config',
            'interface_unverified',
            'scheduler_failure',
            'state_integrity'
        )
    ),
    CONSTRAINT collection_targets_diagnostic_shape CHECK (
        (
            actual_state = 'waiting' AND
            reason_code IN ('next_cycle', 'scheduler_opportunity', 'transient_failure') AND
            recovery_mode = 'automatic' AND
            next_check_at IS NOT NULL AND isfinite(next_check_at) AND
            next_check_at > changed_at
        ) OR
        (
            actual_state = 'blocked' AND
            reason_code IN (
                'no_combination',
                'cooldown',
                'egress_unavailable',
                'missing_rate_policy',
                'session_invalid',
                'invalid_config',
                'interface_unverified'
            ) AND
            (
                (
                    reason_code IN ('no_combination', 'cooldown', 'egress_unavailable') AND
                    recovery_mode = 'automatic' AND
                    next_check_at IS NOT NULL AND isfinite(next_check_at) AND
                    next_check_at > changed_at
                ) OR
                (
                    reason_code IN ('missing_rate_policy', 'session_invalid', 'invalid_config', 'interface_unverified') AND
                    recovery_mode = 'manual' AND
                    next_check_at IS NULL
                )
            )
        ) OR
        (
            actual_state = 'error' AND
            reason_code IN ('scheduler_failure', 'state_integrity') AND
            recovery_mode = 'manual' AND
            next_check_at IS NULL
        ) OR
        (
            actual_state IN ('starting', 'running', 'stopping', 'stopped') AND
            reason_code = '' AND
            recovery_mode = '' AND
            next_check_at IS NULL
        )
    ),
    CONSTRAINT collection_targets_revision_positive CHECK (revision > 0),
    CONSTRAINT collection_targets_switch_version_positive CHECK (switch_version > 0),
    CONSTRAINT collection_targets_next_run_sequence_positive CHECK (next_run_sequence > 0),
    CONSTRAINT collection_targets_changed_at_finite CHECK (isfinite(changed_at))
);

CREATE UNIQUE INDEX collection_targets_catalog_identity_key
    ON collection_targets (platform, appid)
    WHERE kind = 'catalog';

CREATE UNIQUE INDEX collection_targets_summary_identity_key
    ON collection_targets (platform, side)
    WHERE kind = 'summary';

CREATE TABLE collection_runs (
    run_id              BIGINT GENERATED ALWAYS AS IDENTITY,
    target_id           BIGINT,
    kind                TEXT COLLATE "C" NOT NULL,
    platform            TEXT COLLATE "C" NOT NULL,
    appid               BIGINT NOT NULL,
    side                TEXT COLLATE "C",
    product_id          BIGINT,
    switch_version      BIGINT,
    run_sequence        BIGINT NOT NULL,
    status              TEXT COLLATE "C" NOT NULL,
    completeness        TEXT COLLATE "C",
    reason_code         TEXT COLLATE "C" NOT NULL DEFAULT '',
    current_cursor      BYTEA NOT NULL,
    last_page_sequence  BIGINT NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL,
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ,
    CONSTRAINT collection_runs_pkey PRIMARY KEY (run_id),
    CONSTRAINT collection_runs_target_sequence_key UNIQUE (target_id, run_sequence),
    CONSTRAINT collection_runs_target_fkey
        FOREIGN KEY (target_id) REFERENCES collection_targets (target_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT collection_runs_detail_product_fkey
        FOREIGN KEY (product_id, appid) REFERENCES steam_products (product_id, appid)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT collection_runs_identity_positive CHECK (
        run_id > 0 AND appid > 0 AND (target_id IS NULL OR target_id > 0) AND
        (product_id IS NULL OR product_id > 0)
    ),
    CONSTRAINT collection_runs_kind_valid CHECK (kind IN ('catalog', 'summary', 'detail')),
    CONSTRAINT collection_runs_platform_canonical CHECK (
        platform ~ '^[a-z][a-z0-9_.-]{0,31}$'
    ),
    CONSTRAINT collection_runs_side_valid CHECK (side IS NULL OR side IN ('bid', 'ask')),
    CONSTRAINT collection_runs_kind_shape CHECK (
        (
            kind = 'catalog' AND
            target_id IS NOT NULL AND
            platform = 'steam' AND
            side IS NULL AND
            product_id IS NULL AND
            switch_version IS NOT NULL AND
            switch_version > 0
        ) OR
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
    ),
    CONSTRAINT collection_runs_run_sequence_positive CHECK (run_sequence > 0),
    CONSTRAINT collection_runs_status_valid CHECK (
        status IN ('pending', 'running', 'succeeded', 'failed', 'stopped')
    ),
    CONSTRAINT collection_runs_completeness_valid CHECK (
        completeness IS NULL OR completeness IN ('complete', 'partial')
    ),
    CONSTRAINT collection_runs_reason_valid CHECK (
        reason_code = '' OR reason_code IN (
            'network_error',
            'platform_error',
            'timeout',
            'login_invalid',
            'parse_error',
            'semantic_error',
            'configuration_error',
            'internal_error',
            'process_restarted',
            'switch_disabled',
            'cancelled'
        )
    ),
    CONSTRAINT collection_runs_terminal_shape CHECK (
        (
            status = 'pending' AND
            completeness IS NULL AND
            reason_code = '' AND
            last_page_sequence = 0 AND
            started_at IS NULL AND
            finished_at IS NULL
        ) OR
        (
            status = 'running' AND
            completeness IS NULL AND
            reason_code = '' AND
            started_at IS NOT NULL AND
            finished_at IS NULL
        ) OR
        (
            status = 'succeeded' AND
            completeness IS NOT NULL AND
            completeness IN ('complete', 'partial') AND
            reason_code = '' AND
            started_at IS NOT NULL AND
            finished_at IS NOT NULL
        ) OR
        (
            status = 'failed' AND
            completeness IS NOT NULL AND
            completeness IN ('complete', 'partial') AND
            reason_code IN (
                'network_error',
                'platform_error',
                'timeout',
                'login_invalid',
                'parse_error',
                'semantic_error',
                'configuration_error',
                'internal_error',
                'process_restarted'
            ) AND
            (started_at IS NOT NULL OR completeness = 'partial') AND
            finished_at IS NOT NULL
        ) OR
        (
            status = 'stopped' AND
            completeness IS NOT NULL AND
            completeness IN ('complete', 'partial') AND
            reason_code IN ('switch_disabled', 'cancelled') AND
            (started_at IS NOT NULL OR completeness = 'partial') AND
            finished_at IS NOT NULL
        )
    ),
    CONSTRAINT collection_runs_cursor_size CHECK (octet_length(current_cursor) <= 4096),
    CONSTRAINT collection_runs_last_page_sequence_nonnegative CHECK (last_page_sequence >= 0),
    CONSTRAINT collection_runs_page_progress_shape CHECK (
        last_page_sequence = 0 OR started_at IS NOT NULL
    ),
    CONSTRAINT collection_runs_complete_has_page CHECK (
        completeness IS DISTINCT FROM 'complete' OR last_page_sequence > 0
    ),
    CONSTRAINT collection_runs_succeeded_has_page CHECK (
        status <> 'succeeded' OR last_page_sequence > 0
    ),
    CONSTRAINT collection_runs_times_valid CHECK (
        isfinite(created_at) AND
        (started_at IS NULL OR (isfinite(started_at) AND started_at >= created_at)) AND
        (finished_at IS NULL OR (isfinite(finished_at) AND finished_at >= created_at)) AND
        (started_at IS NULL OR finished_at IS NULL OR finished_at >= started_at)
    )
);

CREATE UNIQUE INDEX collection_runs_active_catalog_key
    ON collection_runs (target_id)
    WHERE kind = 'catalog' AND status IN ('pending', 'running');

CREATE UNIQUE INDEX collection_runs_active_summary_key
    ON collection_runs (target_id, appid)
    WHERE kind = 'summary' AND status IN ('pending', 'running');

CREATE UNIQUE INDEX collection_runs_active_detail_key
    ON collection_runs (platform, appid, side, product_id)
    WHERE kind = 'detail' AND status IN ('pending', 'running');

CREATE TABLE collection_pages (
    run_id              BIGINT NOT NULL,
    page_sequence       BIGINT NOT NULL,
    cursor_before       BYTEA NOT NULL,
    cursor_after        BYTEA NOT NULL,
    payload_digest      BYTEA NOT NULL,
    collected_at        TIMESTAMPTZ NOT NULL,
    committed_at        TIMESTAMPTZ NOT NULL,
    CONSTRAINT collection_pages_pkey PRIMARY KEY (run_id, page_sequence),
    CONSTRAINT collection_pages_run_fkey
        FOREIGN KEY (run_id) REFERENCES collection_runs (run_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT collection_pages_run_id_positive CHECK (run_id > 0),
    CONSTRAINT collection_pages_page_sequence_positive CHECK (page_sequence > 0),
    CONSTRAINT collection_pages_cursor_size CHECK (
        octet_length(cursor_before) <= 4096 AND octet_length(cursor_after) <= 4096
    ),
    CONSTRAINT collection_pages_payload_digest_canonical CHECK (
        octet_length(payload_digest) = 32 AND
        payload_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT collection_pages_times_valid CHECK (
        isfinite(collected_at) AND isfinite(committed_at) AND committed_at >= collected_at
    )
);
