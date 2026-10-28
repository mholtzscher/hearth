-- +goose Up
-- Core executes this script after converting retained failures in the same transaction.
ALTER TABLE automation_history DROP COLUMN value_evidence_bytes;
ALTER TABLE automation_history DROP COLUMN value_evidence_records;

-- +goose Down
CREATE TEMP TABLE automation_value_contract_rollback_guard (id INTEGER);
-- +goose StatementBegin
CREATE TEMP TRIGGER automation_value_contract_rollback_abort BEFORE INSERT ON automation_value_contract_rollback_guard
BEGIN
    SELECT RAISE(ABORT, 'Restore the pre-upgrade backup with its matching binary; value failures have no lossless Down migration');
END;
-- +goose StatementEnd
INSERT INTO automation_value_contract_rollback_guard VALUES (1);
