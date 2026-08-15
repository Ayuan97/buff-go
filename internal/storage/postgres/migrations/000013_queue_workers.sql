-- 去掉批次，改成每方向一条有序队列。行情因果序改为 (switch_version, write_seq)。
-- 组合不再要求节点已划方向。私人库允许破坏性迁。

ALTER TABLE collection_targets
    ADD COLUMN refill_cursor BYTEA NOT NULL DEFAULT ''::bytea,
    ADD COLUMN write_seq BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN refill_total BIGINT NOT NULL DEFAULT 0;

UPDATE collection_targets t
SET refill_cursor = r.current_cursor
FROM collection_runs r
WHERE r.target_id = t.target_id
  AND r.kind = 'summary'
  AND r.status IN ('pending', 'running');

ALTER TABLE collection_targets
    DROP CONSTRAINT collection_targets_next_run_sequence_positive;

ALTER TABLE collection_targets
    DROP COLUMN next_run_sequence;

ALTER TABLE collection_targets
    ADD CONSTRAINT collection_targets_write_seq_nonnegative CHECK (write_seq >= 0),
    ADD CONSTRAINT collection_targets_refill_total_nonnegative CHECK (refill_total >= 0),
    ADD CONSTRAINT collection_targets_refill_cursor_size CHECK (octet_length(refill_cursor) <= 4096);

CREATE TABLE collection_tasks (
    task_id      BIGINT GENERATED ALWAYS AS IDENTITY,
    target_id    BIGINT NOT NULL,
    enqueue_seq  BIGINT NOT NULL,
    enqueued_at  TIMESTAMPTZ NOT NULL,
    kind         TEXT COLLATE "C" NOT NULL,
    payload      JSONB NOT NULL,
    state        TEXT COLLATE "C" NOT NULL,
    claimed_by   BIGINT,
    claimed_at   TIMESTAMPTZ,
    CONSTRAINT collection_tasks_pkey PRIMARY KEY (task_id),
    CONSTRAINT collection_tasks_target_enqueue_key UNIQUE (target_id, enqueue_seq),
    CONSTRAINT collection_tasks_target_fkey
        FOREIGN KEY (target_id) REFERENCES collection_targets (target_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT collection_tasks_claimed_by_fkey
        FOREIGN KEY (claimed_by) REFERENCES account_node_combinations (combination_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT collection_tasks_identity_positive CHECK (task_id > 0 AND target_id > 0 AND enqueue_seq > 0),
    CONSTRAINT collection_tasks_kind_valid CHECK (kind IN ('ask_page', 'bid_batch')),
    CONSTRAINT collection_tasks_state_valid CHECK (state IN ('queued', 'claimed')),
    CONSTRAINT collection_tasks_claim_shape CHECK (
        (state = 'queued' AND claimed_by IS NULL AND claimed_at IS NULL) OR
        (state = 'claimed' AND claimed_by IS NOT NULL AND claimed_by > 0 AND claimed_at IS NOT NULL AND isfinite(claimed_at))
    ),
    CONSTRAINT collection_tasks_enqueued_at_valid CHECK (isfinite(enqueued_at))
);

CREATE INDEX collection_tasks_claim_order
    ON collection_tasks (enqueued_at, task_id)
    WHERE state = 'queued';

CREATE INDEX collection_tasks_claimed_by
    ON collection_tasks (claimed_by)
    WHERE state = 'claimed';

CREATE TABLE collection_latest_pages (
    target_id       BIGINT NOT NULL,
    write_seq       BIGINT NOT NULL,
    cursor_before   BYTEA NOT NULL,
    cursor_after    BYTEA NOT NULL,
    payload_digest  BYTEA NOT NULL,
    collected_at    TIMESTAMPTZ NOT NULL,
    committed_at    TIMESTAMPTZ NOT NULL,
    account_id      BIGINT,
    exit_address    INET,
    payload_gzip    BYTEA,
    payload_bytes   BIGINT,
    CONSTRAINT collection_latest_pages_pkey PRIMARY KEY (target_id),
    CONSTRAINT collection_latest_pages_target_fkey
        FOREIGN KEY (target_id) REFERENCES collection_targets (target_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT collection_latest_pages_write_seq_positive CHECK (write_seq > 0),
    CONSTRAINT collection_latest_pages_cursor_size CHECK (
        octet_length(cursor_before) <= 4096 AND octet_length(cursor_after) <= 4096
    ),
    CONSTRAINT collection_latest_pages_payload_digest_canonical CHECK (
        octet_length(payload_digest) = 32 AND
        payload_digest <> decode(repeat('00', 32), 'hex')
    ),
    CONSTRAINT collection_latest_pages_times_valid CHECK (
        isfinite(collected_at) AND isfinite(committed_at) AND committed_at >= collected_at
    ),
    CONSTRAINT collection_latest_pages_account_id_positive CHECK (
        account_id IS NULL OR account_id > 0
    ),
    CONSTRAINT collection_latest_pages_exit_host_address CHECK (
        exit_address IS NULL OR
        (family(exit_address) = 4 AND masklen(exit_address) = 32) OR
        (family(exit_address) = 6 AND masklen(exit_address) = 128)
    ),
    CONSTRAINT collection_latest_pages_payload_shape CHECK (
        (payload_gzip IS NULL AND payload_bytes IS NULL) OR
        (
            payload_gzip IS NOT NULL AND
            octet_length(payload_gzip) BETWEEN 1 AND 1048576 AND
            payload_bytes IS NOT NULL AND
            payload_bytes > 0
        )
    )
);

INSERT INTO collection_latest_pages (
    target_id, write_seq, cursor_before, cursor_after, payload_digest,
    collected_at, committed_at, account_id, exit_address, payload_gzip, payload_bytes
)
SELECT DISTINCT ON (r.target_id)
    r.target_id,
    r.run_sequence * 1000000 + p.page_sequence,
    p.cursor_before,
    p.cursor_after,
    p.payload_digest,
    p.collected_at,
    p.committed_at,
    p.account_id,
    p.exit_address,
    pl.payload_gzip,
    pl.byte_size
FROM collection_pages p
JOIN collection_runs r ON r.run_id = p.run_id
LEFT JOIN collection_page_payloads pl
    ON pl.run_id = p.run_id AND pl.page_sequence = p.page_sequence
WHERE r.target_id IS NOT NULL
ORDER BY r.target_id, r.run_sequence DESC, p.page_sequence DESC;

UPDATE collection_targets t
SET write_seq = p.write_seq
FROM collection_latest_pages p
WHERE p.target_id = t.target_id;

ALTER TABLE market_latest_attempts
    ADD COLUMN write_seq BIGINT;

UPDATE market_latest_attempts
SET write_seq = run_sequence * 1000000 + page_sequence;

ALTER TABLE market_latest_attempts
    ALTER COLUMN write_seq SET NOT NULL;

ALTER TABLE market_latest_attempts
    DROP CONSTRAINT market_latest_attempts_run_sequence_positive,
    DROP CONSTRAINT market_latest_attempts_page_sequence_positive;

ALTER TABLE market_latest_attempts
    DROP COLUMN run_sequence,
    DROP COLUMN page_sequence;

ALTER TABLE market_latest_attempts
    ADD CONSTRAINT market_latest_attempts_write_seq_positive CHECK (write_seq > 0);

ALTER TABLE market_last_present
    ADD COLUMN write_seq BIGINT;

UPDATE market_last_present
SET write_seq = run_sequence * 1000000 + page_sequence;

ALTER TABLE market_last_present
    ALTER COLUMN write_seq SET NOT NULL;

ALTER TABLE market_last_present
    DROP CONSTRAINT market_last_present_run_sequence_positive,
    DROP CONSTRAINT market_last_present_page_sequence_positive;

ALTER TABLE market_last_present
    DROP COLUMN run_sequence,
    DROP COLUMN page_sequence;

ALTER TABLE market_last_present
    ADD CONSTRAINT market_last_present_write_seq_positive CHECK (write_seq > 0);

DROP TABLE collection_page_payloads;
DROP TABLE collection_pages;
DROP TABLE collection_runs;

ALTER TABLE account_node_combinations
    DROP CONSTRAINT IF EXISTS account_node_combinations_node_direction_fkey;
