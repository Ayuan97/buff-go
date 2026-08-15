-- 工人只看平台和地域。节点不再划游戏、划方向。
-- 000013 去掉划方向外键后，组合对节点没有引用约束；这里补回。

ALTER TABLE account_node_combinations
    ADD CONSTRAINT account_node_combinations_node_fkey
        FOREIGN KEY (node_id) REFERENCES access_nodes (node_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT;

DROP TABLE node_direction_assignments;

ALTER TABLE access_nodes
    DROP CONSTRAINT access_nodes_appid_positive,
    DROP CONSTRAINT access_nodes_assignment_revision_positive,
    DROP COLUMN appid,
    DROP COLUMN assignment_revision;
