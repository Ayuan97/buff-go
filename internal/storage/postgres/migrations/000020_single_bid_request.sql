-- 求购任务一次只发一个订单簿请求；旧批次队列必须从目录起点重建。
SELECT pg_advisory_xact_lock(hashtextextended('collection-daemon:' || current_schema(), 0));

DELETE FROM collection_tasks t
USING collection_targets g
WHERE g.target_id = t.target_id
  AND g.side = 'bid';

UPDATE collection_targets
SET refill_cursor = convert_to('0', 'UTF8')
WHERE side = 'bid';
