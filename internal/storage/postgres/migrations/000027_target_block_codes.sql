ALTER TABLE collection_targets
    DROP CONSTRAINT collection_targets_reason_valid,
    DROP CONSTRAINT collection_targets_diagnostic_shape;

ALTER TABLE collection_targets
    ADD CONSTRAINT collection_targets_reason_valid CHECK (
        reason_code = '' OR reason_code IN (
            'next_cycle',
            'scheduler_opportunity',
            'transient_failure',
            'no_combination',
            'cooldown',
            'egress_unavailable',
            'egress_cn_blocked',
            'resource_incomplete',
            'missing_rate_policy',
            'session_invalid',
            'invalid_config',
            'interface_unverified',
            'scheduler_failure',
            'state_integrity'
        )
    );

ALTER TABLE collection_targets
    ADD CONSTRAINT collection_targets_diagnostic_shape CHECK (
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
                'egress_cn_blocked',
                'resource_incomplete',
                'missing_rate_policy',
                'session_invalid',
                'invalid_config',
                'interface_unverified'
            ) AND
            (
                (
                    reason_code IN ('no_combination', 'cooldown', 'egress_unavailable', 'egress_cn_blocked', 'resource_incomplete') AND
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
    );
