-- +goose Up
-- Receipts retain only the identity pair needed after explanatory history is pruned.
ALTER TABLE automation_fact_receipts DROP COLUMN outcome_kind;
ALTER TABLE automation_fact_receipts DROP COLUMN history_id;

-- Effective availability owns the active history pointer, not raw Entity reports.
ALTER TABLE entity_availability_current DROP COLUMN latest_transition_receive_order;

-- +goose Down
CREATE TEMP TABLE unused_columns_rollback_guard (id INTEGER);
-- +goose StatementBegin
CREATE TEMP TRIGGER unused_columns_rollback_abort BEFORE INSERT ON unused_columns_rollback_guard
BEGIN
    SELECT RAISE(ABORT, 'Restore the pre-upgrade backup with its matching binary; removed metadata has no lossless Down migration');
END;
-- +goose StatementEnd
INSERT INTO unused_columns_rollback_guard VALUES (1);
