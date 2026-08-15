-- 一个组合同时只能认领一条任务。迁移时回队现有认领：重启后本来也没有活租约。

UPDATE collection_tasks
SET state = 'queued', claimed_by = NULL, claimed_at = NULL
WHERE state = 'claimed';

DROP INDEX collection_tasks_claimed_by;

CREATE UNIQUE INDEX collection_tasks_claimed_by
    ON collection_tasks (claimed_by)
    WHERE state = 'claimed';
