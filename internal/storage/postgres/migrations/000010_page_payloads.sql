-- 页面原始响应，供控制台下钻查看。只保留最近若干页，写入时 FIFO 淘汰，
-- 所以这张表是可丢弃的诊断副本，不是采集事实来源。
CREATE TABLE collection_page_payloads (
    run_id              BIGINT NOT NULL,
    page_sequence       BIGINT NOT NULL,
    payload_gzip        BYTEA NOT NULL,
    byte_size           BIGINT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL,
    CONSTRAINT collection_page_payloads_pkey PRIMARY KEY (run_id, page_sequence),
    CONSTRAINT collection_page_payloads_page_fkey
        FOREIGN KEY (run_id, page_sequence)
        REFERENCES collection_pages (run_id, page_sequence)
        ON UPDATE RESTRICT ON DELETE CASCADE,
    CONSTRAINT collection_page_payloads_run_id_positive CHECK (run_id > 0),
    CONSTRAINT collection_page_payloads_page_sequence_positive CHECK (page_sequence > 0),
    -- 压缩后单页上限 1 MiB：实测一页约 1.7 KiB，超出这个量级说明抓到了非预期内容
    CONSTRAINT collection_page_payloads_gzip_size CHECK (
        octet_length(payload_gzip) BETWEEN 1 AND 1048576
    ),
    CONSTRAINT collection_page_payloads_byte_size_positive CHECK (byte_size > 0),
    CONSTRAINT collection_page_payloads_created_at_valid CHECK (isfinite(created_at))
);

-- FIFO 淘汰按写入时间倒序取，主键补足全序避免同一时刻的行序不定
CREATE INDEX collection_page_payloads_recent
    ON collection_page_payloads (created_at DESC, run_id DESC, page_sequence DESC);
