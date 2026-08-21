UPDATE access_nodes
SET state = 'unavailable',
    exit_address = NULL,
    exit_verified_revision = NULL,
    exit_verified_at = NULL,
    exit_valid_until = NULL
WHERE exit_address <<= '192.0.2.0/24'::inet
   OR exit_address <<= '198.51.100.0/24'::inet
   OR exit_address <<= '203.0.113.0/24'::inet
   OR exit_address <<= '2001:db8::/32'::inet;

ALTER TABLE access_nodes
    ADD CONSTRAINT access_nodes_exit_not_documentation CHECK (
        exit_address IS NULL OR NOT (
            exit_address <<= '192.0.2.0/24'::inet OR
            exit_address <<= '198.51.100.0/24'::inet OR
            exit_address <<= '203.0.113.0/24'::inet OR
            exit_address <<= '2001:db8::/32'::inet
        )
    );
