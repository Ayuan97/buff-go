ALTER TABLE collection_tasks
    ADD COLUMN claim_generation BIGINT NOT NULL DEFAULT 0;

UPDATE collection_tasks
SET claim_generation = 1
WHERE state = 'claimed';

ALTER TABLE collection_tasks
    ADD CONSTRAINT collection_tasks_claim_generation_valid CHECK (
        claim_generation >= 0 AND
        (state <> 'claimed' OR claim_generation > 0)
    );
