CREATE TABLE collection_daemon_ownership (
    singleton   BOOLEAN PRIMARY KEY DEFAULT TRUE,
    owner_epoch BIGINT NOT NULL DEFAULT 0,
    CONSTRAINT collection_daemon_ownership_singleton CHECK (singleton),
    CONSTRAINT collection_daemon_ownership_epoch_nonnegative CHECK (owner_epoch >= 0)
);

INSERT INTO collection_daemon_ownership (singleton, owner_epoch)
VALUES (TRUE, 0);
