CREATE TABLE platform_accounts (
    account_id                    BIGINT GENERATED ALWAYS AS IDENTITY,
    platform                      TEXT COLLATE "C" NOT NULL,
    alias                         TEXT COLLATE "C" NOT NULL,
    session_state                 TEXT COLLATE "C" NOT NULL DEFAULT 'unverified',
    session_revision              BIGINT NOT NULL DEFAULT 1,
    last_checked_at               TIMESTAMPTZ,
    session_envelope_version      SMALLINT NOT NULL,
    session_key_id                TEXT COLLATE "C" NOT NULL,
    session_nonce                 BYTEA NOT NULL,
    session_ciphertext            BYTEA NOT NULL,
    CONSTRAINT platform_accounts_pkey PRIMARY KEY (account_id),
    CONSTRAINT platform_accounts_platform_alias_key UNIQUE (platform, alias),
    CONSTRAINT platform_accounts_account_platform_key UNIQUE (account_id, platform),
    CONSTRAINT platform_accounts_account_id_positive CHECK (account_id > 0),
    CONSTRAINT platform_accounts_platform_canonical
        CHECK (platform ~ '^[a-z][a-z0-9_.-]{0,31}$'),
    CONSTRAINT platform_accounts_alias_length
        CHECK (octet_length(alias) BETWEEN 1 AND 128),
    CONSTRAINT platform_accounts_alias_trimmed CHECK (alias = btrim(alias)),
    CONSTRAINT platform_accounts_alias_no_control CHECK (alias !~ '[[:cntrl:]]'),
    CONSTRAINT platform_accounts_session_state_valid
        CHECK (session_state IN ('unverified', 'valid', 'invalid')),
    CONSTRAINT platform_accounts_session_revision_positive CHECK (session_revision > 0),
    CONSTRAINT platform_accounts_session_check_consistent CHECK (
        (session_state = 'unverified' AND last_checked_at IS NULL) OR
        (session_state IN ('valid', 'invalid') AND last_checked_at IS NOT NULL AND isfinite(last_checked_at))
    ),
    CONSTRAINT platform_accounts_session_envelope_version_v1
        CHECK (session_envelope_version = 1),
    CONSTRAINT platform_accounts_session_key_id_canonical CHECK (
        octet_length(session_key_id) BETWEEN 1 AND 64 AND
        session_key_id ~ '^[a-z0-9][a-z0-9_.-]{0,63}$'
    ),
    CONSTRAINT platform_accounts_session_nonce_12
        CHECK (octet_length(session_nonce) = 12),
    CONSTRAINT platform_accounts_session_ciphertext_nonempty
        CHECK (octet_length(session_ciphertext) > 16)
);

CREATE TABLE access_nodes (
    node_id                       BIGINT GENERATED ALWAYS AS IDENTITY,
    name                          TEXT COLLATE "C" NOT NULL,
    kind                          TEXT COLLATE "C" NOT NULL,
    region                        TEXT COLLATE "C" NOT NULL,
    egress_mode                   TEXT COLLATE "C" NOT NULL,
    state                         TEXT COLLATE "C" NOT NULL DEFAULT 'validating',
    egress_revision               BIGINT NOT NULL DEFAULT 1,
    assigned_platform             TEXT COLLATE "C",
    proxy_envelope_version        SMALLINT,
    proxy_key_id                  TEXT COLLATE "C",
    proxy_nonce                   BYTEA,
    proxy_ciphertext              BYTEA,
    sticky_session_valid_until    TIMESTAMPTZ,
    exit_address                  INET,
    exit_verified_revision        BIGINT,
    exit_verified_at              TIMESTAMPTZ,
    exit_valid_until              TIMESTAMPTZ,
    CONSTRAINT access_nodes_pkey PRIMARY KEY (node_id),
    CONSTRAINT access_nodes_name_key UNIQUE (name),
    CONSTRAINT access_nodes_node_platform_key UNIQUE (node_id, assigned_platform),
    CONSTRAINT access_nodes_node_id_positive CHECK (node_id > 0),
    CONSTRAINT access_nodes_name_length CHECK (octet_length(name) BETWEEN 1 AND 128),
    CONSTRAINT access_nodes_name_trimmed CHECK (name = btrim(name)),
    CONSTRAINT access_nodes_name_no_control CHECK (name !~ '[[:cntrl:]]'),
    CONSTRAINT access_nodes_kind_valid CHECK (kind IN ('direct', 'proxy')),
    CONSTRAINT access_nodes_region_valid CHECK (region IN ('domestic', 'foreign', 'hongkong')),
    CONSTRAINT access_nodes_egress_mode_valid CHECK (egress_mode IN ('static', 'sticky')),
    CONSTRAINT access_nodes_state_valid CHECK (state IN ('validating', 'available', 'unavailable')),
    CONSTRAINT access_nodes_egress_revision_positive CHECK (egress_revision > 0),
    CONSTRAINT access_nodes_assigned_platform_canonical CHECK (
        assigned_platform IS NULL OR
        assigned_platform ~ '^[a-z][a-z0-9_.-]{0,31}$'
    ),
    CONSTRAINT access_nodes_direct_static CHECK (kind <> 'direct' OR egress_mode = 'static'),
    CONSTRAINT access_nodes_proxy_envelope_consistent CHECK (
        (
            kind = 'direct' AND
            proxy_envelope_version IS NULL AND proxy_key_id IS NULL AND
            proxy_nonce IS NULL AND proxy_ciphertext IS NULL
        ) OR
        (
            kind = 'proxy' AND
            proxy_envelope_version IS NOT NULL AND
            proxy_key_id IS NOT NULL AND
            proxy_nonce IS NOT NULL AND
            proxy_ciphertext IS NOT NULL AND
            proxy_envelope_version = 1 AND
            octet_length(proxy_key_id) BETWEEN 1 AND 64 AND
            proxy_key_id ~ '^[a-z0-9][a-z0-9_.-]{0,63}$' AND
            octet_length(proxy_nonce) = 12 AND
            octet_length(proxy_ciphertext) > 16
        )
    ),
    CONSTRAINT access_nodes_sticky_session_consistent CHECK (
        (
            egress_mode = 'static' AND
            sticky_session_valid_until IS NULL
        ) OR
        (
            egress_mode = 'sticky' AND
            sticky_session_valid_until IS NOT NULL AND
            isfinite(sticky_session_valid_until)
        )
    ),
    CONSTRAINT access_nodes_exit_host_address CHECK (
        exit_address IS NULL OR
        (family(exit_address) = 4 AND masklen(exit_address) = 32) OR
        (family(exit_address) = 6 AND masklen(exit_address) = 128)
    ),
    CONSTRAINT access_nodes_exit_public_unicast CHECK (
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
    CONSTRAINT access_nodes_exit_state_consistent CHECK (
        (
            state = 'available' AND
            exit_address IS NOT NULL AND
            exit_verified_revision IS NOT NULL AND
            exit_verified_revision = egress_revision AND
            exit_verified_at IS NOT NULL AND isfinite(exit_verified_at) AND
            exit_valid_until IS NOT NULL AND isfinite(exit_valid_until) AND
            exit_valid_until > exit_verified_at
        ) OR
        (
            state IN ('validating', 'unavailable') AND
            exit_address IS NULL AND exit_verified_revision IS NULL AND
            exit_verified_at IS NULL AND exit_valid_until IS NULL
        )
    ),
    CONSTRAINT access_nodes_sticky_exit_bound CHECK (
        sticky_session_valid_until IS NULL OR
        exit_valid_until IS NULL OR
        exit_valid_until <= sticky_session_valid_until
    )
);
