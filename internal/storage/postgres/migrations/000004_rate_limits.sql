CREATE TABLE rate_limit_policies (
    policy_id                       BIGINT GENERATED ALWAYS AS IDENTITY,
    platform                        TEXT COLLATE "C" NOT NULL,
    rule_key                        TEXT COLLATE "C" NOT NULL,
    scope                           TEXT COLLATE "C" NOT NULL,
    endpoint_class                  TEXT COLLATE "C" NOT NULL DEFAULT '',
    kind                            TEXT COLLATE "C" NOT NULL,
    min_interval_microseconds       BIGINT,
    window_microseconds             BIGINT,
    max_requests                    BIGINT,
    default_cooldown_microseconds   BIGINT,
    active                          BOOLEAN NOT NULL,
    revision                        BIGINT NOT NULL DEFAULT 1,
    changed_at                      TIMESTAMPTZ NOT NULL,
    ready_at                        TIMESTAMPTZ NOT NULL,
    CONSTRAINT rate_limit_policies_pkey PRIMARY KEY (policy_id),
    CONSTRAINT rate_limit_policies_platform_rule_key UNIQUE (platform, rule_key),
    CONSTRAINT rate_limit_policies_policy_scope_kind_key UNIQUE (policy_id, scope, kind),
    CONSTRAINT rate_limit_policies_identity_positive CHECK (policy_id > 0),
    CONSTRAINT rate_limit_policies_platform_canonical CHECK (
        platform ~ '^[a-z][a-z0-9_.-]{0,31}$'
    ),
    CONSTRAINT rate_limit_policies_rule_key_canonical CHECK (
        rule_key ~ '^[a-z][a-z0-9_.-]{0,63}$'
    ),
    CONSTRAINT rate_limit_policies_scope_valid CHECK (
        scope IN ('platform', 'interface', 'account', 'ip', 'account_ip')
    ),
    CONSTRAINT rate_limit_policies_endpoint_shape CHECK (
        (scope = 'interface' AND endpoint_class ~ '^[a-z][a-z0-9_.-]{0,63}$') OR
        (scope <> 'interface' AND endpoint_class = '')
    ),
    CONSTRAINT rate_limit_policies_kind_valid CHECK (
        kind IN ('cooldown_only', 'min_interval', 'rolling_window')
    ),
    CONSTRAINT rate_limit_policies_duration_bounds CHECK (
        (min_interval_microseconds IS NULL OR min_interval_microseconds BETWEEN 1 AND 31536000000000) AND
        (window_microseconds IS NULL OR window_microseconds BETWEEN 1 AND 31536000000000) AND
        (default_cooldown_microseconds IS NULL OR default_cooldown_microseconds BETWEEN 1 AND 31536000000000)
    ),
    CONSTRAINT rate_limit_policies_kind_shape CHECK (
        (
            kind = 'cooldown_only' AND
            min_interval_microseconds IS NULL AND
            window_microseconds IS NULL AND
            max_requests IS NULL
        ) OR
        (
            kind = 'min_interval' AND
            min_interval_microseconds IS NOT NULL AND
            window_microseconds IS NULL AND
            max_requests IS NULL
        ) OR
        (
            kind = 'rolling_window' AND
            min_interval_microseconds IS NULL AND
            window_microseconds IS NOT NULL AND
            max_requests BETWEEN 1 AND 1024
        )
    ),
    CONSTRAINT rate_limit_policies_revision_positive CHECK (revision > 0),
    CONSTRAINT rate_limit_policies_times_valid CHECK (
        isfinite(changed_at) AND isfinite(ready_at) AND ready_at >= changed_at
    )
);

CREATE FUNCTION rate_limit_rolling_state_valid(
    timestamps TIMESTAMPTZ[],
    last_admitted TIMESTAMPTZ,
    clock_floor TIMESTAMPTZ
) RETURNS BOOLEAN
LANGUAGE SQL
IMMUTABLE
PARALLEL SAFE
SET search_path = pg_catalog
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

CREATE TABLE rate_limit_states (
    state_id                        BIGINT GENERATED ALWAYS AS IDENTITY,
    policy_id                       BIGINT NOT NULL,
    scope                           TEXT COLLATE "C" NOT NULL,
    kind                            TEXT COLLATE "C" NOT NULL,
    account_id                      BIGINT,
    exit_address                    INET,
    policy_revision                 BIGINT NOT NULL,
    next_allowed_at                 TIMESTAMPTZ,
    rolling_admitted_at             TIMESTAMPTZ[] NOT NULL DEFAULT ARRAY[]::TIMESTAMPTZ[],
    last_admitted_at                TIMESTAMPTZ,
    clock_floor_at                  TIMESTAMPTZ NOT NULL,
    cooldown_until                  TIMESTAMPTZ,
    cooldown_observed_at            TIMESTAMPTZ,
    cooldown_reason_code            TEXT COLLATE "C",
    CONSTRAINT rate_limit_states_pkey PRIMARY KEY (state_id),
    CONSTRAINT rate_limit_states_identity_positive CHECK (
        state_id > 0 AND policy_id > 0 AND (account_id IS NULL OR account_id > 0)
    ),
    CONSTRAINT rate_limit_states_policy_scope_kind_fkey
        FOREIGN KEY (policy_id, scope, kind)
        REFERENCES rate_limit_policies (policy_id, scope, kind)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    CONSTRAINT rate_limit_states_scope_valid CHECK (
        scope IN ('platform', 'interface', 'account', 'ip', 'account_ip')
    ),
    CONSTRAINT rate_limit_states_kind_valid CHECK (
        kind IN ('cooldown_only', 'min_interval', 'rolling_window')
    ),
    CONSTRAINT rate_limit_states_subject_shape CHECK (
        (scope IN ('platform', 'interface') AND account_id IS NULL AND exit_address IS NULL) OR
        (scope = 'account' AND account_id IS NOT NULL AND exit_address IS NULL) OR
        (scope = 'ip' AND account_id IS NULL AND exit_address IS NOT NULL) OR
        (scope = 'account_ip' AND account_id IS NOT NULL AND exit_address IS NOT NULL)
    ),
    CONSTRAINT rate_limit_states_exit_host_address CHECK (
        exit_address IS NULL OR
        (family(exit_address) = 4 AND masklen(exit_address) = 32) OR
        (family(exit_address) = 6 AND masklen(exit_address) = 128)
    ),
    CONSTRAINT rate_limit_states_exit_public_unicast CHECK (
        exit_address IS NULL OR NOT (
            exit_address <<= '0.0.0.0/8'::inet OR
            exit_address <<= '10.0.0.0/8'::inet OR
            exit_address <<= '100.64.0.0/10'::inet OR
            exit_address <<= '127.0.0.0/8'::inet OR
            exit_address <<= '169.254.0.0/16'::inet OR
            exit_address <<= '172.16.0.0/12'::inet OR
            exit_address <<= '192.168.0.0/16'::inet OR
            exit_address <<= '224.0.0.0/4'::inet OR
            exit_address <<= '240.0.0.0/4'::inet OR
            exit_address <<= '::/128'::inet OR
            exit_address <<= '::1/128'::inet OR
            exit_address <<= '::ffff:0:0/96'::inet OR
            exit_address <<= 'fc00::/7'::inet OR
            exit_address <<= 'fe80::/10'::inet OR
            exit_address <<= 'ff00::/8'::inet
        )
    ),
    CONSTRAINT rate_limit_states_policy_revision_positive CHECK (policy_revision > 0),
    CONSTRAINT rate_limit_states_time_shape CHECK (
        (next_allowed_at IS NULL OR isfinite(next_allowed_at)) AND
        (last_admitted_at IS NULL OR isfinite(last_admitted_at)) AND
        isfinite(clock_floor_at) AND
        (last_admitted_at IS NULL OR last_admitted_at <= clock_floor_at)
    ),
    CONSTRAINT rate_limit_states_quota_shape CHECK (
        (
            kind = 'cooldown_only' AND
            next_allowed_at IS NULL AND
            cardinality(rolling_admitted_at) = 0 AND
            last_admitted_at IS NULL
        ) OR
        (
            kind = 'min_interval' AND
            cardinality(rolling_admitted_at) = 0 AND
            (
                (next_allowed_at IS NULL AND last_admitted_at IS NULL) OR
                (next_allowed_at IS NOT NULL AND last_admitted_at IS NOT NULL AND next_allowed_at > last_admitted_at)
            )
        ) OR
        (
            kind = 'rolling_window' AND
            next_allowed_at IS NULL AND
            (
                (cardinality(rolling_admitted_at) = 0 AND last_admitted_at IS NULL) OR
                (cardinality(rolling_admitted_at) > 0 AND last_admitted_at IS NOT NULL)
            )
        )
    ),
    CONSTRAINT rate_limit_states_rolling_shape CHECK (
        cardinality(rolling_admitted_at) <= 1024 AND
        (
            cardinality(rolling_admitted_at) = 0 OR
            (array_ndims(rolling_admitted_at) = 1 AND array_lower(rolling_admitted_at, 1) = 1)
        ) AND
        array_position(rolling_admitted_at, NULL) IS NULL AND
        NOT ('infinity'::TIMESTAMPTZ = ANY(rolling_admitted_at)) AND
        NOT ('-infinity'::TIMESTAMPTZ = ANY(rolling_admitted_at)) AND
        rate_limit_rolling_state_valid(
            rolling_admitted_at,
            last_admitted_at,
            clock_floor_at
        )
    ),
    CONSTRAINT rate_limit_states_cooldown_shape CHECK (
        (
            cooldown_until IS NULL AND
            cooldown_observed_at IS NULL AND
            cooldown_reason_code IS NULL
        ) OR
        (
            cooldown_until IS NOT NULL AND isfinite(cooldown_until) AND
            cooldown_observed_at IS NOT NULL AND isfinite(cooldown_observed_at) AND
            cooldown_until > cooldown_observed_at AND
            cooldown_observed_at <= clock_floor_at AND
            cooldown_reason_code IN ('http_429', 'risk_control')
        )
    )
);

CREATE UNIQUE INDEX rate_limit_states_global_key
    ON rate_limit_states (policy_id)
    WHERE scope IN ('platform', 'interface');

CREATE UNIQUE INDEX rate_limit_states_account_key
    ON rate_limit_states (policy_id, account_id)
    WHERE scope = 'account';

CREATE UNIQUE INDEX rate_limit_states_ip_key
    ON rate_limit_states (policy_id, exit_address)
    WHERE scope = 'ip';

CREATE UNIQUE INDEX rate_limit_states_account_ip_key
    ON rate_limit_states (policy_id, account_id, exit_address)
    WHERE scope = 'account_ip';

CREATE INDEX rate_limit_states_policy_clock_idx
    ON rate_limit_states (policy_id, clock_floor_at DESC);
