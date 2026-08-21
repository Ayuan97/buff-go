-- 当前库结构的只读快照。
--
-- 事实来源是 internal/storage/postgres/migrations/，程序启动时按文件名顺序应用。
-- 改结构只新增迁移；应用全部迁移后重新导出本文件：
--
--   pg_dump --schema-only --no-owner --no-privileges "$BUFFGO_DSN" -f docs/schema.sql
--
-- 导出自已应用到 000026_reject_documentation_exit 的库，不包含业务数据。
-- buffgo_storage_migrations 是迁移执行器维护的版本记录表。

--
-- PostgreSQL database dump
--

\restrict BuffgoSchemaSnapshot000025OwnerEpochFence202608210000000000000000

-- Dumped from database version 17.5 (Homebrew)
-- Dumped by pg_dump version 18.0

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: rate_limit_rolling_state_valid(timestamp with time zone[], timestamp with time zone, timestamp with time zone); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.rate_limit_rolling_state_valid(timestamps timestamp with time zone[], last_admitted timestamp with time zone, clock_floor timestamp with time zone) RETURNS boolean
    LANGUAGE sql IMMUTABLE PARALLEL SAFE
    SET search_path TO 'pg_catalog'
    AS $$
    SELECT CASE
        WHEN cardinality(timestamps) = 0 THEN TRUE
        WHEN last_admitted IS NULL THEN FALSE
        ELSE
            last_admitted = timestamps[array_upper(timestamps, 1)] AND
            NOT EXISTS (
                SELECT 1
                FROM generate_subscripts(timestamps, 1) AS position
                WHERE timestamps[position] > clock_floor
                   OR (
                       position > array_lower(timestamps, 1) AND
                       timestamps[position] < timestamps[position - 1]
                   )
            )
    END
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: access_nodes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.access_nodes (
    node_id bigint NOT NULL,
    name text NOT NULL COLLATE pg_catalog."C",
    kind text NOT NULL COLLATE pg_catalog."C",
    region text NOT NULL COLLATE pg_catalog."C",
    egress_mode text NOT NULL COLLATE pg_catalog."C",
    state text DEFAULT 'validating'::text NOT NULL COLLATE pg_catalog."C",
    egress_revision bigint DEFAULT 1 NOT NULL,
    sticky_session_valid_until timestamp with time zone,
    exit_address inet,
    exit_verified_revision bigint,
    exit_verified_at timestamp with time zone,
    exit_valid_until timestamp with time zone,
    proxy_plaintext bytea,
    CONSTRAINT access_nodes_direct_static CHECK (((kind <> 'direct'::text) OR (egress_mode = 'static'::text))),
    CONSTRAINT access_nodes_egress_mode_valid CHECK ((egress_mode = ANY (ARRAY['static'::text, 'sticky'::text]))),
    CONSTRAINT access_nodes_egress_revision_positive CHECK ((egress_revision > 0)),
    CONSTRAINT access_nodes_exit_host_address CHECK (((exit_address IS NULL) OR ((family(exit_address) = 4) AND (masklen(exit_address) = 32)) OR ((family(exit_address) = 6) AND (masklen(exit_address) = 128)))),
    CONSTRAINT access_nodes_exit_not_documentation CHECK (((exit_address IS NULL) OR (NOT ((exit_address <<= '192.0.2.0/24'::inet) OR (exit_address <<= '198.51.100.0/24'::inet) OR (exit_address <<= '203.0.113.0/24'::inet) OR (exit_address <<= '2001:db8::/32'::inet))))),
    CONSTRAINT access_nodes_exit_public_unicast CHECK (((exit_address IS NULL) OR (NOT ((exit_address <<= '0.0.0.0/8'::inet) OR (exit_address <<= '10.0.0.0/8'::inet) OR (exit_address <<= '100.64.0.0/10'::inet) OR (exit_address <<= '127.0.0.0/8'::inet) OR (exit_address <<= '169.254.0.0/16'::inet) OR (exit_address <<= '172.16.0.0/12'::inet) OR (exit_address <<= '192.168.0.0/16'::inet) OR (exit_address <<= '224.0.0.0/4'::inet) OR (exit_address <<= '240.0.0.0/4'::inet) OR (exit_address <<= '::'::inet) OR (exit_address <<= '::1'::inet) OR (exit_address <<= '::ffff:0.0.0.0/96'::inet) OR (exit_address <<= 'fc00::/7'::inet) OR (exit_address <<= 'fe80::/10'::inet) OR (exit_address <<= 'ff00::/8'::inet))))),
    CONSTRAINT access_nodes_exit_state_consistent CHECK ((((state = 'available'::text) AND (exit_address IS NOT NULL) AND (exit_verified_revision IS NOT NULL) AND (exit_verified_revision = egress_revision) AND (exit_verified_at IS NOT NULL) AND isfinite(exit_verified_at) AND (exit_valid_until IS NOT NULL) AND isfinite(exit_valid_until) AND (exit_valid_until > exit_verified_at)) OR ((state = ANY (ARRAY['validating'::text, 'unavailable'::text])) AND (exit_address IS NULL) AND (exit_verified_revision IS NULL) AND (exit_verified_at IS NULL) AND (exit_valid_until IS NULL)))),
    CONSTRAINT access_nodes_kind_valid CHECK ((kind = ANY (ARRAY['direct'::text, 'proxy'::text]))),
    CONSTRAINT access_nodes_name_length CHECK (((octet_length(name) >= 1) AND (octet_length(name) <= 128))),
    CONSTRAINT access_nodes_name_no_control CHECK ((name !~ '[[:cntrl:]]'::text)),
    CONSTRAINT access_nodes_name_trimmed CHECK ((name = btrim(name))),
    CONSTRAINT access_nodes_node_id_positive CHECK ((node_id > 0)),
    CONSTRAINT access_nodes_proxy_plaintext_consistent CHECK ((((kind = 'direct'::text) AND (proxy_plaintext IS NULL)) OR ((kind = 'proxy'::text) AND (proxy_plaintext IS NOT NULL) AND (octet_length(proxy_plaintext) > 0)))),
    CONSTRAINT access_nodes_region_valid CHECK ((region = ANY (ARRAY['domestic'::text, 'foreign'::text, 'hongkong'::text]))),
    CONSTRAINT access_nodes_state_valid CHECK ((state = ANY (ARRAY['validating'::text, 'available'::text, 'unavailable'::text]))),
    CONSTRAINT access_nodes_sticky_exit_bound CHECK (((sticky_session_valid_until IS NULL) OR (exit_valid_until IS NULL) OR (exit_valid_until <= sticky_session_valid_until))),
    CONSTRAINT access_nodes_sticky_session_consistent CHECK ((((egress_mode = 'static'::text) AND (sticky_session_valid_until IS NULL)) OR ((egress_mode = 'sticky'::text) AND (sticky_session_valid_until IS NOT NULL) AND isfinite(sticky_session_valid_until))))
);


--
-- Name: access_nodes_node_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.access_nodes ALTER COLUMN node_id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.access_nodes_node_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: account_node_combinations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.account_node_combinations (
    combination_id bigint NOT NULL,
    platform text NOT NULL COLLATE pg_catalog."C",
    account_id bigint NOT NULL,
    node_id bigint NOT NULL,
    CONSTRAINT account_node_combinations_identity_positive CHECK (((combination_id > 0) AND (account_id > 0) AND (node_id > 0))),
    CONSTRAINT account_node_combinations_platform_token CHECK ((platform ~ '^[a-z][a-z0-9_.-]{0,31}$'::text))
);


--
-- Name: account_node_combinations_combination_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.account_node_combinations ALTER COLUMN combination_id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.account_node_combinations_combination_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: buffgo_storage_migrations; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.buffgo_storage_migrations (
    version text NOT NULL,
    checksum text NOT NULL,
    applied_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT buffgo_storage_migrations_applied_at_check CHECK (isfinite(applied_at)),
    CONSTRAINT buffgo_storage_migrations_checksum_check CHECK ((checksum ~ '^[0-9a-f]{64}$'::text)),
    CONSTRAINT buffgo_storage_migrations_version_check CHECK ((version <> ''::text))
);


--
-- Name: collection_daemon_ownership; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.collection_daemon_ownership (
    singleton boolean DEFAULT true NOT NULL,
    owner_epoch bigint DEFAULT 0 NOT NULL,
    CONSTRAINT collection_daemon_ownership_epoch_nonnegative CHECK ((owner_epoch >= 0)),
    CONSTRAINT collection_daemon_ownership_singleton CHECK (singleton)
);


--
-- Name: collection_latest_pages; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.collection_latest_pages (
    target_id bigint NOT NULL,
    write_seq bigint NOT NULL,
    cursor_before bytea NOT NULL,
    cursor_after bytea NOT NULL,
    payload_digest bytea NOT NULL,
    collected_at timestamp with time zone NOT NULL,
    committed_at timestamp with time zone NOT NULL,
    account_id bigint,
    exit_address inet,
    payload_gzip bytea,
    payload_bytes bigint,
    switch_version bigint NOT NULL,
    CONSTRAINT collection_latest_pages_account_id_positive CHECK (((account_id IS NULL) OR (account_id > 0))),
    CONSTRAINT collection_latest_pages_cursor_size CHECK (((octet_length(cursor_before) <= 4096) AND (octet_length(cursor_after) <= 4096))),
    CONSTRAINT collection_latest_pages_exit_host_address CHECK (((exit_address IS NULL) OR ((family(exit_address) = 4) AND (masklen(exit_address) = 32)) OR ((family(exit_address) = 6) AND (masklen(exit_address) = 128)))),
    CONSTRAINT collection_latest_pages_payload_digest_canonical CHECK (((octet_length(payload_digest) = 32) AND (payload_digest <> decode(repeat('00'::text, 32), 'hex'::text)))),
    CONSTRAINT collection_latest_pages_payload_shape CHECK ((((payload_gzip IS NULL) AND (payload_bytes IS NULL)) OR ((payload_gzip IS NOT NULL) AND ((octet_length(payload_gzip) >= 1) AND (octet_length(payload_gzip) <= 1048576)) AND (payload_bytes IS NOT NULL) AND (payload_bytes > 0)))),
    CONSTRAINT collection_latest_pages_switch_version_nonnegative CHECK ((switch_version >= 0)),
    CONSTRAINT collection_latest_pages_times_valid CHECK ((isfinite(collected_at) AND isfinite(committed_at) AND (committed_at >= collected_at))),
    CONSTRAINT collection_latest_pages_write_seq_positive CHECK ((write_seq > 0))
);


--
-- Name: collection_targets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.collection_targets (
    target_id bigint NOT NULL,
    kind text NOT NULL COLLATE pg_catalog."C",
    platform text NOT NULL COLLATE pg_catalog."C",
    appid bigint,
    side text COLLATE pg_catalog."C",
    desired_state text NOT NULL COLLATE pg_catalog."C",
    actual_state text NOT NULL COLLATE pg_catalog."C",
    reason_code text DEFAULT ''::text NOT NULL COLLATE pg_catalog."C",
    recovery_mode text DEFAULT ''::text NOT NULL COLLATE pg_catalog."C",
    next_check_at timestamp with time zone,
    period_microseconds bigint,
    revision bigint DEFAULT 1 NOT NULL,
    switch_version bigint DEFAULT 1 NOT NULL,
    changed_at timestamp with time zone NOT NULL,
    sort_column text DEFAULT 'price'::text NOT NULL COLLATE pg_catalog."C",
    sort_dir text DEFAULT 'asc'::text NOT NULL COLLATE pg_catalog."C",
    refill_cursor bytea DEFAULT '\x'::bytea NOT NULL,
    write_seq bigint DEFAULT 0 NOT NULL,
    refill_total bigint DEFAULT 0 NOT NULL,
    price_min_cents bigint,
    price_max_cents bigint,
    steam_cats text[] DEFAULT '{}'::text[] NOT NULL,
    item_classes text[] DEFAULT '{}'::text[] NOT NULL,
    CONSTRAINT collection_targets_actual_state_valid CHECK ((actual_state = ANY (ARRAY['starting'::text, 'waiting'::text, 'running'::text, 'blocked'::text, 'stopping'::text, 'stopped'::text, 'error'::text]))),
    CONSTRAINT collection_targets_changed_at_finite CHECK (isfinite(changed_at)),
    CONSTRAINT collection_targets_desired_actual_shape CHECK ((((desired_state = 'enabled'::text) AND (actual_state = ANY (ARRAY['starting'::text, 'waiting'::text, 'running'::text, 'blocked'::text, 'error'::text]))) OR ((desired_state = 'disabled'::text) AND (actual_state = ANY (ARRAY['stopping'::text, 'stopped'::text]))))),
    CONSTRAINT collection_targets_desired_state_valid CHECK ((desired_state = ANY (ARRAY['enabled'::text, 'disabled'::text]))),
    CONSTRAINT collection_targets_diagnostic_shape CHECK ((((actual_state = 'waiting'::text) AND (reason_code = ANY (ARRAY['next_cycle'::text, 'scheduler_opportunity'::text, 'transient_failure'::text])) AND (recovery_mode = 'automatic'::text) AND (next_check_at IS NOT NULL) AND isfinite(next_check_at) AND (next_check_at > changed_at)) OR ((actual_state = 'blocked'::text) AND (reason_code = ANY (ARRAY['no_combination'::text, 'cooldown'::text, 'egress_unavailable'::text, 'missing_rate_policy'::text, 'session_invalid'::text, 'invalid_config'::text, 'interface_unverified'::text])) AND (((reason_code = ANY (ARRAY['no_combination'::text, 'cooldown'::text, 'egress_unavailable'::text])) AND (recovery_mode = 'automatic'::text) AND (next_check_at IS NOT NULL) AND isfinite(next_check_at) AND (next_check_at > changed_at)) OR ((reason_code = ANY (ARRAY['missing_rate_policy'::text, 'session_invalid'::text, 'invalid_config'::text, 'interface_unverified'::text])) AND (recovery_mode = 'manual'::text) AND (next_check_at IS NULL)))) OR ((actual_state = 'error'::text) AND (reason_code = ANY (ARRAY['scheduler_failure'::text, 'state_integrity'::text])) AND (recovery_mode = 'manual'::text) AND (next_check_at IS NULL)) OR ((actual_state = ANY (ARRAY['starting'::text, 'running'::text, 'stopping'::text, 'stopped'::text])) AND (reason_code = ''::text) AND (recovery_mode = ''::text) AND (next_check_at IS NULL)))),
    CONSTRAINT collection_targets_identity_positive CHECK ((target_id > 0)),
    CONSTRAINT collection_targets_item_classes_shape CHECK ((cardinality(item_classes) <= 128)),
    CONSTRAINT collection_targets_kind_shape CHECK (((kind = 'summary'::text) AND (appid IS NOT NULL) AND (appid > 0) AND (side IS NOT NULL) AND (side = ANY (ARRAY['bid'::text, 'ask'::text])) AND (period_microseconds IS NULL))),
    CONSTRAINT collection_targets_kind_valid CHECK ((kind = 'summary'::text)),
    CONSTRAINT collection_targets_platform_canonical CHECK ((platform ~ '^[a-z][a-z0-9_.-]{0,31}$'::text)),
    CONSTRAINT collection_targets_price_range_valid CHECK ((((price_min_cents IS NULL) OR (price_min_cents >= 0)) AND ((price_max_cents IS NULL) OR (price_max_cents >= 0)) AND ((price_min_cents IS NULL) OR (price_max_cents IS NULL) OR (price_min_cents <= price_max_cents)))),
    CONSTRAINT collection_targets_reason_valid CHECK (((reason_code = ''::text) OR (reason_code = ANY (ARRAY['next_cycle'::text, 'scheduler_opportunity'::text, 'transient_failure'::text, 'no_combination'::text, 'cooldown'::text, 'egress_unavailable'::text, 'missing_rate_policy'::text, 'session_invalid'::text, 'invalid_config'::text, 'interface_unverified'::text, 'scheduler_failure'::text, 'state_integrity'::text])))),
    CONSTRAINT collection_targets_recovery_mode_valid CHECK ((recovery_mode = ANY (ARRAY[''::text, 'automatic'::text, 'manual'::text]))),
    CONSTRAINT collection_targets_refill_cursor_size CHECK ((octet_length(refill_cursor) <= 4096)),
    CONSTRAINT collection_targets_refill_total_nonnegative CHECK ((refill_total >= 0)),
    CONSTRAINT collection_targets_revision_positive CHECK ((revision > 0)),
    CONSTRAINT collection_targets_side_valid CHECK (((side IS NULL) OR (side = ANY (ARRAY['bid'::text, 'ask'::text])))),
    CONSTRAINT collection_targets_sort_column_valid CHECK ((sort_column = ANY (ARRAY['price'::text, 'quantity'::text, 'name'::text]))),
    CONSTRAINT collection_targets_sort_dir_valid CHECK ((sort_dir = ANY (ARRAY['asc'::text, 'desc'::text]))),
    CONSTRAINT collection_targets_steam_cats_shape CHECK ((cardinality(steam_cats) <= 8)),
    CONSTRAINT collection_targets_switch_version_positive CHECK ((switch_version > 0)),
    CONSTRAINT collection_targets_write_seq_nonnegative CHECK ((write_seq >= 0))
);


--
-- Name: collection_targets_target_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.collection_targets ALTER COLUMN target_id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.collection_targets_target_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: collection_tasks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.collection_tasks (
    task_id bigint NOT NULL,
    target_id bigint NOT NULL,
    enqueue_seq bigint NOT NULL,
    enqueued_at timestamp with time zone NOT NULL,
    kind text NOT NULL COLLATE pg_catalog."C",
    payload jsonb NOT NULL,
    state text NOT NULL COLLATE pg_catalog."C",
    claimed_by bigint,
    claimed_at timestamp with time zone,
    claim_generation bigint DEFAULT 0 NOT NULL,
    CONSTRAINT collection_tasks_claim_generation_valid CHECK (((claim_generation >= 0) AND ((state <> 'claimed'::text) OR (claim_generation > 0)))),
    CONSTRAINT collection_tasks_claim_shape CHECK ((((state = 'queued'::text) AND (claimed_by IS NULL) AND (claimed_at IS NULL)) OR ((state = 'claimed'::text) AND (claimed_by IS NOT NULL) AND (claimed_by > 0) AND (claimed_at IS NOT NULL) AND isfinite(claimed_at)))),
    CONSTRAINT collection_tasks_enqueued_at_valid CHECK (isfinite(enqueued_at)),
    CONSTRAINT collection_tasks_identity_positive CHECK (((task_id > 0) AND (target_id > 0) AND (enqueue_seq > 0))),
    CONSTRAINT collection_tasks_kind_valid CHECK ((kind = ANY (ARRAY['ask_page'::text, 'bid_batch'::text]))),
    CONSTRAINT collection_tasks_state_valid CHECK ((state = ANY (ARRAY['queued'::text, 'claimed'::text])))
);


--
-- Name: collection_tasks_task_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.collection_tasks ALTER COLUMN task_id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.collection_tasks_task_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: market_last_present; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_last_present (
    product_id bigint NOT NULL,
    platform text NOT NULL COLLATE pg_catalog."C",
    side text NOT NULL COLLATE pg_catalog."C",
    price_cny_cents bigint NOT NULL,
    order_count bigint,
    item_count bigint,
    source_time timestamp with time zone,
    collected_at timestamp with time zone NOT NULL,
    switch_version bigint NOT NULL,
    write_seq bigint NOT NULL,
    CONSTRAINT market_last_present_collected_at_finite CHECK (isfinite(collected_at)),
    CONSTRAINT market_last_present_item_count_nonnegative CHECK (((item_count IS NULL) OR (item_count >= 0))),
    CONSTRAINT market_last_present_order_count_nonnegative CHECK (((order_count IS NULL) OR (order_count >= 0))),
    CONSTRAINT market_last_present_platform_canonical CHECK ((platform ~ '^[a-z][a-z0-9_.-]{0,31}$'::text)),
    CONSTRAINT market_last_present_price_nonnegative CHECK ((price_cny_cents >= 0)),
    CONSTRAINT market_last_present_product_id_positive CHECK ((product_id > 0)),
    CONSTRAINT market_last_present_side_valid CHECK ((side = ANY (ARRAY['bid'::text, 'ask'::text]))),
    CONSTRAINT market_last_present_source_time_finite CHECK (((source_time IS NULL) OR isfinite(source_time))),
    CONSTRAINT market_last_present_switch_version_positive CHECK ((switch_version > 0)),
    CONSTRAINT market_last_present_write_seq_positive CHECK ((write_seq > 0))
);


--
-- Name: market_latest_attempts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_latest_attempts (
    product_id bigint NOT NULL,
    platform text NOT NULL COLLATE pg_catalog."C",
    side text NOT NULL COLLATE pg_catalog."C",
    status text NOT NULL COLLATE pg_catalog."C",
    reason_code text DEFAULT ''::text NOT NULL COLLATE pg_catalog."C",
    source_time timestamp with time zone,
    collected_at timestamp with time zone NOT NULL,
    switch_version bigint NOT NULL,
    write_seq bigint NOT NULL,
    CONSTRAINT market_latest_attempts_collected_at_finite CHECK (isfinite(collected_at)),
    CONSTRAINT market_latest_attempts_platform_canonical CHECK ((platform ~ '^[a-z][a-z0-9_.-]{0,31}$'::text)),
    CONSTRAINT market_latest_attempts_product_id_positive CHECK ((product_id > 0)),
    CONSTRAINT market_latest_attempts_reason_valid CHECK ((((status = ANY (ARRAY['present'::text, 'empty'::text])) AND (reason_code = ''::text)) OR ((status = ANY (ARRAY['unavailable'::text, 'failed'::text])) AND (reason_code ~ '^[a-z][a-z0-9_.-]{0,63}$'::text)))),
    CONSTRAINT market_latest_attempts_side_valid CHECK ((side = ANY (ARRAY['bid'::text, 'ask'::text]))),
    CONSTRAINT market_latest_attempts_source_time_finite CHECK (((source_time IS NULL) OR isfinite(source_time))),
    CONSTRAINT market_latest_attempts_status_valid CHECK ((status = ANY (ARRAY['present'::text, 'empty'::text, 'unavailable'::text, 'failed'::text]))),
    CONSTRAINT market_latest_attempts_switch_version_positive CHECK ((switch_version > 0)),
    CONSTRAINT market_latest_attempts_write_seq_positive CHECK ((write_seq > 0))
);


--
-- Name: market_price_ticks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.market_price_ticks (
    tick_id bigint NOT NULL,
    product_id bigint NOT NULL,
    platform text NOT NULL COLLATE pg_catalog."C",
    side text NOT NULL COLLATE pg_catalog."C",
    prev_cents bigint,
    price_cny_cents bigint NOT NULL,
    collected_at timestamp with time zone NOT NULL,
    switch_version bigint NOT NULL,
    write_seq bigint NOT NULL,
    CONSTRAINT market_price_ticks_cents_changed CHECK (((prev_cents IS NULL) OR (prev_cents IS DISTINCT FROM price_cny_cents))),
    CONSTRAINT market_price_ticks_collected_at_finite CHECK (isfinite(collected_at)),
    CONSTRAINT market_price_ticks_platform_canonical CHECK ((platform ~ '^[a-z][a-z0-9_.-]{0,31}$'::text)),
    CONSTRAINT market_price_ticks_prev_nonnegative CHECK (((prev_cents IS NULL) OR (prev_cents >= 0))),
    CONSTRAINT market_price_ticks_price_nonnegative CHECK ((price_cny_cents >= 0)),
    CONSTRAINT market_price_ticks_product_id_positive CHECK ((product_id > 0)),
    CONSTRAINT market_price_ticks_side_valid CHECK ((side = ANY (ARRAY['bid'::text, 'ask'::text]))),
    CONSTRAINT market_price_ticks_switch_version_positive CHECK ((switch_version > 0)),
    CONSTRAINT market_price_ticks_write_seq_positive CHECK ((write_seq > 0))
);


--
-- Name: market_price_ticks_tick_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.market_price_ticks ALTER COLUMN tick_id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.market_price_ticks_tick_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: platform_accounts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.platform_accounts (
    account_id bigint NOT NULL,
    platform text NOT NULL COLLATE pg_catalog."C",
    alias text NOT NULL COLLATE pg_catalog."C",
    session_state text DEFAULT 'unverified'::text NOT NULL COLLATE pg_catalog."C",
    session_revision bigint DEFAULT 1 NOT NULL,
    last_checked_at timestamp with time zone,
    session_plaintext bytea NOT NULL,
    CONSTRAINT platform_accounts_account_id_positive CHECK ((account_id > 0)),
    CONSTRAINT platform_accounts_alias_length CHECK (((octet_length(alias) >= 1) AND (octet_length(alias) <= 128))),
    CONSTRAINT platform_accounts_alias_no_control CHECK ((alias !~ '[[:cntrl:]]'::text)),
    CONSTRAINT platform_accounts_alias_trimmed CHECK ((alias = btrim(alias))),
    CONSTRAINT platform_accounts_platform_canonical CHECK ((platform ~ '^[a-z][a-z0-9_.-]{0,31}$'::text)),
    CONSTRAINT platform_accounts_session_check_consistent CHECK ((((session_state = 'unverified'::text) AND (last_checked_at IS NULL)) OR ((session_state = ANY (ARRAY['valid'::text, 'invalid'::text])) AND (last_checked_at IS NOT NULL) AND isfinite(last_checked_at)))),
    CONSTRAINT platform_accounts_session_plaintext_nonempty CHECK ((octet_length(session_plaintext) > 0)),
    CONSTRAINT platform_accounts_session_revision_positive CHECK ((session_revision > 0)),
    CONSTRAINT platform_accounts_session_state_valid CHECK ((session_state = ANY (ARRAY['unverified'::text, 'valid'::text, 'invalid'::text])))
);


--
-- Name: platform_accounts_account_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.platform_accounts ALTER COLUMN account_id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.platform_accounts_account_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: platform_product_mappings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.platform_product_mappings (
    platform text NOT NULL COLLATE pg_catalog."C",
    appid bigint NOT NULL,
    platform_item_id text NOT NULL COLLATE pg_catalog."C",
    product_id bigint NOT NULL,
    CONSTRAINT platform_product_mappings_appid_positive CHECK ((appid > 0)),
    CONSTRAINT platform_product_mappings_platform_canonical CHECK ((platform ~ '^[a-z][a-z0-9_.-]{0,31}$'::text)),
    CONSTRAINT platform_product_mappings_platform_item_id_nonempty CHECK ((platform_item_id <> ''::text)),
    CONSTRAINT platform_product_mappings_product_id_positive CHECK ((product_id > 0))
);


--
-- Name: proxy_provider_regions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.proxy_provider_regions (
    provider_id bigint NOT NULL,
    region text NOT NULL COLLATE pg_catalog."C",
    CONSTRAINT proxy_provider_regions_region_valid CHECK ((region = ANY (ARRAY['domestic'::text, 'foreign'::text, 'hongkong'::text])))
);


--
-- Name: proxy_providers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.proxy_providers (
    provider_id bigint NOT NULL,
    name text NOT NULL COLLATE pg_catalog."C",
    enabled boolean DEFAULT true NOT NULL,
    priority integer NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    credential_plaintext bytea NOT NULL,
    CONSTRAINT proxy_providers_credential_nonempty CHECK ((octet_length(credential_plaintext) > 0)),
    CONSTRAINT proxy_providers_name_length CHECK (((octet_length(name) >= 1) AND (octet_length(name) <= 128))),
    CONSTRAINT proxy_providers_name_no_control CHECK ((name !~ '[[:cntrl:]]'::text)),
    CONSTRAINT proxy_providers_name_trimmed CHECK ((name = btrim(name))),
    CONSTRAINT proxy_providers_priority_range CHECK (((priority >= 1) AND (priority <= 10000))),
    CONSTRAINT proxy_providers_provider_id_positive CHECK ((provider_id > 0)),
    CONSTRAINT proxy_providers_revision_positive CHECK ((revision > 0))
);


--
-- Name: proxy_providers_provider_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.proxy_providers ALTER COLUMN provider_id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.proxy_providers_provider_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: rate_limit_policies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.rate_limit_policies (
    policy_id bigint NOT NULL,
    platform text NOT NULL COLLATE pg_catalog."C",
    rule_key text NOT NULL COLLATE pg_catalog."C",
    scope text NOT NULL COLLATE pg_catalog."C",
    endpoint_class text DEFAULT ''::text NOT NULL COLLATE pg_catalog."C",
    kind text NOT NULL COLLATE pg_catalog."C",
    min_interval_microseconds bigint,
    window_microseconds bigint,
    max_requests bigint,
    default_cooldown_microseconds bigint,
    active boolean NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    changed_at timestamp with time zone NOT NULL,
    ready_at timestamp with time zone NOT NULL,
    CONSTRAINT rate_limit_policies_duration_bounds CHECK ((((min_interval_microseconds IS NULL) OR ((min_interval_microseconds >= 1) AND (min_interval_microseconds <= '31536000000000'::bigint))) AND ((window_microseconds IS NULL) OR ((window_microseconds >= 1) AND (window_microseconds <= '31536000000000'::bigint))) AND ((default_cooldown_microseconds IS NULL) OR ((default_cooldown_microseconds >= 1) AND (default_cooldown_microseconds <= '31536000000000'::bigint))))),
    CONSTRAINT rate_limit_policies_endpoint_shape CHECK ((((scope = 'interface'::text) AND (endpoint_class ~ '^[a-z][a-z0-9_.-]{0,63}$'::text)) OR ((scope = 'account_ip'::text) AND ((endpoint_class = ''::text) OR (endpoint_class ~ '^[a-z][a-z0-9_.-]{0,63}$'::text))) OR ((scope <> ALL (ARRAY['interface'::text, 'account_ip'::text])) AND (endpoint_class = ''::text)))),
    CONSTRAINT rate_limit_policies_identity_positive CHECK ((policy_id > 0)),
    CONSTRAINT rate_limit_policies_kind_shape CHECK ((((kind = 'cooldown_only'::text) AND (min_interval_microseconds IS NULL) AND (window_microseconds IS NULL) AND (max_requests IS NULL)) OR ((kind = 'min_interval'::text) AND (min_interval_microseconds IS NOT NULL) AND (window_microseconds IS NULL) AND (max_requests IS NULL)) OR ((kind = 'rolling_window'::text) AND (min_interval_microseconds IS NULL) AND (window_microseconds IS NOT NULL) AND ((max_requests >= 1) AND (max_requests <= 1024))))),
    CONSTRAINT rate_limit_policies_kind_valid CHECK ((kind = ANY (ARRAY['cooldown_only'::text, 'min_interval'::text, 'rolling_window'::text]))),
    CONSTRAINT rate_limit_policies_platform_canonical CHECK ((platform ~ '^[a-z][a-z0-9_.-]{0,31}$'::text)),
    CONSTRAINT rate_limit_policies_revision_positive CHECK ((revision > 0)),
    CONSTRAINT rate_limit_policies_rule_key_canonical CHECK ((rule_key ~ '^[a-z][a-z0-9_.-]{0,63}$'::text)),
    CONSTRAINT rate_limit_policies_scope_valid CHECK ((scope = ANY (ARRAY['platform'::text, 'interface'::text, 'account'::text, 'ip'::text, 'account_ip'::text]))),
    CONSTRAINT rate_limit_policies_times_valid CHECK ((isfinite(changed_at) AND isfinite(ready_at) AND (ready_at >= changed_at)))
);


--
-- Name: rate_limit_policies_policy_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.rate_limit_policies ALTER COLUMN policy_id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.rate_limit_policies_policy_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: rate_limit_states; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.rate_limit_states (
    state_id bigint NOT NULL,
    policy_id bigint NOT NULL,
    scope text NOT NULL COLLATE pg_catalog."C",
    kind text NOT NULL COLLATE pg_catalog."C",
    account_id bigint,
    exit_address inet,
    policy_revision bigint NOT NULL,
    next_allowed_at timestamp with time zone,
    rolling_admitted_at timestamp with time zone[] DEFAULT ARRAY[]::timestamp with time zone[] NOT NULL,
    last_admitted_at timestamp with time zone,
    clock_floor_at timestamp with time zone NOT NULL,
    cooldown_until timestamp with time zone,
    cooldown_observed_at timestamp with time zone,
    cooldown_reason_code text COLLATE pg_catalog."C",
    CONSTRAINT rate_limit_states_cooldown_shape CHECK ((((cooldown_until IS NULL) AND (cooldown_observed_at IS NULL) AND (cooldown_reason_code IS NULL)) OR ((cooldown_until IS NOT NULL) AND isfinite(cooldown_until) AND (cooldown_observed_at IS NOT NULL) AND isfinite(cooldown_observed_at) AND (cooldown_until > cooldown_observed_at) AND (cooldown_observed_at <= clock_floor_at) AND (cooldown_reason_code = ANY (ARRAY['http_429'::text, 'risk_control'::text]))))),
    CONSTRAINT rate_limit_states_exit_host_address CHECK (((exit_address IS NULL) OR ((family(exit_address) = 4) AND (masklen(exit_address) = 32)) OR ((family(exit_address) = 6) AND (masklen(exit_address) = 128)))),
    CONSTRAINT rate_limit_states_exit_public_unicast CHECK (((exit_address IS NULL) OR (NOT ((exit_address <<= '0.0.0.0/8'::inet) OR (exit_address <<= '10.0.0.0/8'::inet) OR (exit_address <<= '100.64.0.0/10'::inet) OR (exit_address <<= '127.0.0.0/8'::inet) OR (exit_address <<= '169.254.0.0/16'::inet) OR (exit_address <<= '172.16.0.0/12'::inet) OR (exit_address <<= '192.168.0.0/16'::inet) OR (exit_address <<= '224.0.0.0/4'::inet) OR (exit_address <<= '240.0.0.0/4'::inet) OR (exit_address <<= '::'::inet) OR (exit_address <<= '::1'::inet) OR (exit_address <<= '::ffff:0.0.0.0/96'::inet) OR (exit_address <<= 'fc00::/7'::inet) OR (exit_address <<= 'fe80::/10'::inet) OR (exit_address <<= 'ff00::/8'::inet))))),
    CONSTRAINT rate_limit_states_identity_positive CHECK (((state_id > 0) AND (policy_id > 0) AND ((account_id IS NULL) OR (account_id > 0)))),
    CONSTRAINT rate_limit_states_kind_valid CHECK ((kind = ANY (ARRAY['cooldown_only'::text, 'min_interval'::text, 'rolling_window'::text]))),
    CONSTRAINT rate_limit_states_policy_revision_positive CHECK ((policy_revision > 0)),
    CONSTRAINT rate_limit_states_quota_shape CHECK ((((kind = 'cooldown_only'::text) AND (next_allowed_at IS NULL) AND (cardinality(rolling_admitted_at) = 0) AND (last_admitted_at IS NULL)) OR ((kind = 'min_interval'::text) AND (cardinality(rolling_admitted_at) = 0) AND (((next_allowed_at IS NULL) AND (last_admitted_at IS NULL)) OR ((next_allowed_at IS NOT NULL) AND (last_admitted_at IS NOT NULL) AND (next_allowed_at > last_admitted_at)))) OR ((kind = 'rolling_window'::text) AND (next_allowed_at IS NULL) AND (((cardinality(rolling_admitted_at) = 0) AND (last_admitted_at IS NULL)) OR ((cardinality(rolling_admitted_at) > 0) AND (last_admitted_at IS NOT NULL)))))),
    CONSTRAINT rate_limit_states_rolling_shape CHECK (((cardinality(rolling_admitted_at) <= 1024) AND ((cardinality(rolling_admitted_at) = 0) OR ((array_ndims(rolling_admitted_at) = 1) AND (array_lower(rolling_admitted_at, 1) = 1))) AND (array_position(rolling_admitted_at, NULL::timestamp with time zone) IS NULL) AND (NOT ('infinity'::timestamp with time zone = ANY (rolling_admitted_at))) AND (NOT ('-infinity'::timestamp with time zone = ANY (rolling_admitted_at))) AND public.rate_limit_rolling_state_valid(rolling_admitted_at, last_admitted_at, clock_floor_at))),
    CONSTRAINT rate_limit_states_scope_valid CHECK ((scope = ANY (ARRAY['platform'::text, 'interface'::text, 'account'::text, 'ip'::text, 'account_ip'::text]))),
    CONSTRAINT rate_limit_states_subject_shape CHECK ((((scope = ANY (ARRAY['platform'::text, 'interface'::text])) AND (account_id IS NULL) AND (exit_address IS NULL)) OR ((scope = 'account'::text) AND (account_id IS NOT NULL) AND (exit_address IS NULL)) OR ((scope = 'ip'::text) AND (account_id IS NULL) AND (exit_address IS NOT NULL)) OR ((scope = 'account_ip'::text) AND (account_id IS NOT NULL) AND (exit_address IS NOT NULL)))),
    CONSTRAINT rate_limit_states_time_shape CHECK ((((next_allowed_at IS NULL) OR isfinite(next_allowed_at)) AND ((last_admitted_at IS NULL) OR isfinite(last_admitted_at)) AND isfinite(clock_floor_at) AND ((last_admitted_at IS NULL) OR (last_admitted_at <= clock_floor_at))))
);


--
-- Name: rate_limit_states_state_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.rate_limit_states ALTER COLUMN state_id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.rate_limit_states_state_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: short_pool_watermarks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.short_pool_watermarks (
    region text NOT NULL COLLATE pg_catalog."C",
    min_usable integer NOT NULL,
    revision bigint DEFAULT 1 NOT NULL,
    CONSTRAINT short_pool_watermarks_min_usable_nonneg CHECK ((min_usable >= 0)),
    CONSTRAINT short_pool_watermarks_region_valid CHECK ((region = ANY (ARRAY['domestic'::text, 'foreign'::text, 'hongkong'::text]))),
    CONSTRAINT short_pool_watermarks_revision_positive CHECK ((revision > 0))
);


--
-- Name: steam_products; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.steam_products (
    product_id bigint NOT NULL,
    appid bigint NOT NULL,
    name text NOT NULL COLLATE pg_catalog."C",
    icon_path text COLLATE pg_catalog."C",
    item_type text COLLATE pg_catalog."C",
    name_color text COLLATE pg_catalog."C",
    CONSTRAINT steam_products_appid_positive CHECK ((appid > 0)),
    CONSTRAINT steam_products_icon_path_shape CHECK (((icon_path IS NULL) OR (((octet_length(icon_path) >= 1) AND (octet_length(icon_path) <= 512)) AND (icon_path !~ '[[:space:][:cntrl:]/]'::text)))),
    CONSTRAINT steam_products_item_type_shape CHECK (((item_type IS NULL) OR (((octet_length(item_type) >= 1) AND (octet_length(item_type) <= 128)) AND (item_type !~ '[[:cntrl:]]'::text)))),
    CONSTRAINT steam_products_name_color_shape CHECK (((name_color IS NULL) OR (name_color ~ '^[0-9a-fA-F]{6}$'::text))),
    CONSTRAINT steam_products_name_nonempty CHECK ((name <> ''::text)),
    CONSTRAINT steam_products_product_id_positive CHECK ((product_id > 0))
);


--
-- Name: steam_products_product_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

ALTER TABLE public.steam_products ALTER COLUMN product_id ADD GENERATED ALWAYS AS IDENTITY (
    SEQUENCE NAME public.steam_products_product_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1
);


--
-- Name: access_nodes access_nodes_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.access_nodes
    ADD CONSTRAINT access_nodes_name_key UNIQUE (name);


--
-- Name: access_nodes access_nodes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.access_nodes
    ADD CONSTRAINT access_nodes_pkey PRIMARY KEY (node_id);


--
-- Name: account_node_combinations account_node_combinations_account_node_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_node_combinations
    ADD CONSTRAINT account_node_combinations_account_node_key UNIQUE (account_id, node_id);


--
-- Name: account_node_combinations account_node_combinations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_node_combinations
    ADD CONSTRAINT account_node_combinations_pkey PRIMARY KEY (combination_id);


--
-- Name: buffgo_storage_migrations buffgo_storage_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.buffgo_storage_migrations
    ADD CONSTRAINT buffgo_storage_migrations_pkey PRIMARY KEY (version);


--
-- Name: collection_daemon_ownership collection_daemon_ownership_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_daemon_ownership
    ADD CONSTRAINT collection_daemon_ownership_pkey PRIMARY KEY (singleton);


--
-- Name: collection_latest_pages collection_latest_pages_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_latest_pages
    ADD CONSTRAINT collection_latest_pages_pkey PRIMARY KEY (target_id);


--
-- Name: collection_targets collection_targets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_targets
    ADD CONSTRAINT collection_targets_pkey PRIMARY KEY (target_id);


--
-- Name: collection_tasks collection_tasks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_tasks
    ADD CONSTRAINT collection_tasks_pkey PRIMARY KEY (task_id);


--
-- Name: collection_tasks collection_tasks_target_enqueue_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_tasks
    ADD CONSTRAINT collection_tasks_target_enqueue_key UNIQUE (target_id, enqueue_seq);


--
-- Name: market_last_present market_last_present_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_last_present
    ADD CONSTRAINT market_last_present_pkey PRIMARY KEY (product_id, platform, side);


--
-- Name: market_latest_attempts market_latest_attempts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_latest_attempts
    ADD CONSTRAINT market_latest_attempts_pkey PRIMARY KEY (product_id, platform, side);


--
-- Name: market_price_ticks market_price_ticks_identity_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_price_ticks
    ADD CONSTRAINT market_price_ticks_identity_key UNIQUE (product_id, platform, side, switch_version, write_seq);


--
-- Name: market_price_ticks market_price_ticks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_price_ticks
    ADD CONSTRAINT market_price_ticks_pkey PRIMARY KEY (tick_id);


--
-- Name: platform_accounts platform_accounts_account_platform_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.platform_accounts
    ADD CONSTRAINT platform_accounts_account_platform_key UNIQUE (account_id, platform);


--
-- Name: platform_accounts platform_accounts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.platform_accounts
    ADD CONSTRAINT platform_accounts_pkey PRIMARY KEY (account_id);


--
-- Name: platform_accounts platform_accounts_platform_alias_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.platform_accounts
    ADD CONSTRAINT platform_accounts_platform_alias_key UNIQUE (platform, alias);


--
-- Name: platform_product_mappings platform_product_mappings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.platform_product_mappings
    ADD CONSTRAINT platform_product_mappings_pkey PRIMARY KEY (platform, appid, platform_item_id);


--
-- Name: proxy_provider_regions proxy_provider_regions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.proxy_provider_regions
    ADD CONSTRAINT proxy_provider_regions_pkey PRIMARY KEY (provider_id, region);


--
-- Name: proxy_providers proxy_providers_name_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.proxy_providers
    ADD CONSTRAINT proxy_providers_name_key UNIQUE (name);


--
-- Name: proxy_providers proxy_providers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.proxy_providers
    ADD CONSTRAINT proxy_providers_pkey PRIMARY KEY (provider_id);


--
-- Name: rate_limit_policies rate_limit_policies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.rate_limit_policies
    ADD CONSTRAINT rate_limit_policies_pkey PRIMARY KEY (policy_id);


--
-- Name: rate_limit_policies rate_limit_policies_platform_rule_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.rate_limit_policies
    ADD CONSTRAINT rate_limit_policies_platform_rule_key UNIQUE (platform, rule_key);


--
-- Name: rate_limit_policies rate_limit_policies_policy_scope_kind_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.rate_limit_policies
    ADD CONSTRAINT rate_limit_policies_policy_scope_kind_key UNIQUE (policy_id, scope, kind);


--
-- Name: rate_limit_states rate_limit_states_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.rate_limit_states
    ADD CONSTRAINT rate_limit_states_pkey PRIMARY KEY (state_id);


--
-- Name: short_pool_watermarks short_pool_watermarks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.short_pool_watermarks
    ADD CONSTRAINT short_pool_watermarks_pkey PRIMARY KEY (region);


--
-- Name: steam_products steam_products_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.steam_products
    ADD CONSTRAINT steam_products_pkey PRIMARY KEY (product_id);


--
-- Name: steam_products steam_products_product_appid_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.steam_products
    ADD CONSTRAINT steam_products_product_appid_key UNIQUE (product_id, appid);


--
-- Name: account_node_combinations_node_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX account_node_combinations_node_id_idx ON public.account_node_combinations USING btree (node_id);


--
-- Name: collection_targets_summary_identity_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX collection_targets_summary_identity_key ON public.collection_targets USING btree (platform, appid, side) WHERE (kind = 'summary'::text);


--
-- Name: collection_tasks_claim_order; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX collection_tasks_claim_order ON public.collection_tasks USING btree (enqueued_at, task_id) WHERE (state = 'queued'::text);


--
-- Name: collection_tasks_claimed_by; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX collection_tasks_claimed_by ON public.collection_tasks USING btree (claimed_by) WHERE (state = 'claimed'::text);


--
-- Name: market_price_ticks_collected_at_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_price_ticks_collected_at_idx ON public.market_price_ticks USING btree (collected_at);


--
-- Name: market_price_ticks_platform_side_time_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_price_ticks_platform_side_time_idx ON public.market_price_ticks USING btree (platform, side, collected_at DESC, tick_id DESC);


--
-- Name: market_price_ticks_series_time_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX market_price_ticks_series_time_idx ON public.market_price_ticks USING btree (product_id, platform, side, collected_at DESC, tick_id DESC);


--
-- Name: rate_limit_states_account_ip_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX rate_limit_states_account_ip_key ON public.rate_limit_states USING btree (policy_id, account_id, exit_address) WHERE (scope = 'account_ip'::text);


--
-- Name: rate_limit_states_account_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX rate_limit_states_account_key ON public.rate_limit_states USING btree (policy_id, account_id) WHERE (scope = 'account'::text);


--
-- Name: rate_limit_states_global_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX rate_limit_states_global_key ON public.rate_limit_states USING btree (policy_id) WHERE (scope = ANY (ARRAY['platform'::text, 'interface'::text]));


--
-- Name: rate_limit_states_ip_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX rate_limit_states_ip_key ON public.rate_limit_states USING btree (policy_id, exit_address) WHERE (scope = 'ip'::text);


--
-- Name: rate_limit_states_policy_clock_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX rate_limit_states_policy_clock_idx ON public.rate_limit_states USING btree (policy_id, clock_floor_at DESC);


--
-- Name: steam_products_appid_item_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX steam_products_appid_item_type ON public.steam_products USING btree (appid, item_type) WHERE (item_type IS NOT NULL);


--
-- Name: steam_products_appid_name_key; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX steam_products_appid_name_key ON public.steam_products USING btree (appid, name);


--
-- Name: steam_products_appid_product_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX steam_products_appid_product_id_idx ON public.steam_products USING btree (appid, product_id);


--
-- Name: account_node_combinations account_node_combinations_account_platform_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_node_combinations
    ADD CONSTRAINT account_node_combinations_account_platform_fkey FOREIGN KEY (account_id, platform) REFERENCES public.platform_accounts(account_id, platform) ON UPDATE RESTRICT ON DELETE RESTRICT;


--
-- Name: account_node_combinations account_node_combinations_node_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_node_combinations
    ADD CONSTRAINT account_node_combinations_node_fkey FOREIGN KEY (node_id) REFERENCES public.access_nodes(node_id) ON UPDATE RESTRICT ON DELETE RESTRICT;


--
-- Name: collection_latest_pages collection_latest_pages_target_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_latest_pages
    ADD CONSTRAINT collection_latest_pages_target_fkey FOREIGN KEY (target_id) REFERENCES public.collection_targets(target_id) ON UPDATE RESTRICT ON DELETE RESTRICT;


--
-- Name: collection_tasks collection_tasks_claimed_by_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_tasks
    ADD CONSTRAINT collection_tasks_claimed_by_fkey FOREIGN KEY (claimed_by) REFERENCES public.account_node_combinations(combination_id) ON UPDATE RESTRICT ON DELETE RESTRICT;


--
-- Name: collection_tasks collection_tasks_target_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.collection_tasks
    ADD CONSTRAINT collection_tasks_target_fkey FOREIGN KEY (target_id) REFERENCES public.collection_targets(target_id) ON UPDATE RESTRICT ON DELETE RESTRICT;


--
-- Name: market_last_present market_last_present_product_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_last_present
    ADD CONSTRAINT market_last_present_product_fkey FOREIGN KEY (product_id) REFERENCES public.steam_products(product_id) ON DELETE RESTRICT;


--
-- Name: market_latest_attempts market_latest_attempts_product_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_latest_attempts
    ADD CONSTRAINT market_latest_attempts_product_fkey FOREIGN KEY (product_id) REFERENCES public.steam_products(product_id) ON DELETE RESTRICT;


--
-- Name: market_price_ticks market_price_ticks_product_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.market_price_ticks
    ADD CONSTRAINT market_price_ticks_product_fkey FOREIGN KEY (product_id) REFERENCES public.steam_products(product_id) ON DELETE RESTRICT;


--
-- Name: platform_product_mappings platform_product_mappings_product_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.platform_product_mappings
    ADD CONSTRAINT platform_product_mappings_product_fkey FOREIGN KEY (product_id, appid) REFERENCES public.steam_products(product_id, appid) ON DELETE RESTRICT;


--
-- Name: proxy_provider_regions proxy_provider_regions_provider_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.proxy_provider_regions
    ADD CONSTRAINT proxy_provider_regions_provider_fkey FOREIGN KEY (provider_id) REFERENCES public.proxy_providers(provider_id) ON UPDATE RESTRICT ON DELETE CASCADE;


--
-- Name: rate_limit_states rate_limit_states_policy_scope_kind_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.rate_limit_states
    ADD CONSTRAINT rate_limit_states_policy_scope_kind_fkey FOREIGN KEY (policy_id, scope, kind) REFERENCES public.rate_limit_policies(policy_id, scope, kind) ON UPDATE RESTRICT ON DELETE RESTRICT;


--
-- PostgreSQL database dump complete
--

\unrestrict BuffgoSchemaSnapshot000025OwnerEpochFence202608210000000000000000
