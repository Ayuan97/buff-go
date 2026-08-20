-- 账号与出口 IP 的限频规则可按平台接口独立分桶。
ALTER TABLE rate_limit_policies
    DROP CONSTRAINT rate_limit_policies_endpoint_shape;

ALTER TABLE rate_limit_policies
    ADD CONSTRAINT rate_limit_policies_endpoint_shape CHECK (
        (scope = 'interface' AND endpoint_class ~ '^[a-z][a-z0-9_.-]{0,63}$') OR
        (
            scope = 'account_ip' AND
            (endpoint_class = '' OR endpoint_class ~ '^[a-z][a-z0-9_.-]{0,63}$')
        ) OR
        (scope NOT IN ('interface', 'account_ip') AND endpoint_class = '')
    );
