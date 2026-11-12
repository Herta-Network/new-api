-- index_system_tasks_by_type_and_id
--
-- Every node reads the newest system task of each sync topic every few
-- seconds, and the scheduler the newest run of each job. An index on
-- (type, id) answers "newest row of a type" without reading the older rows,
-- and serves every lookup by type the index on type alone did, so it
-- replaces that one.

CREATE INDEX idx_system_tasks_type_id ON system_tasks (type, id);
DROP INDEX idx_system_tasks_type;
