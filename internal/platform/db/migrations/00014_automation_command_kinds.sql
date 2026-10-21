-- +goose Up
-- Command-kind conversion now runs in migration 17, in the same transaction as
-- its bounded size allowance. The original version 14 failed on otherwise-valid
-- 64 KiB definitions. Keep this version so both pending and already-applied
-- databases reach the repair without deleting history or migration records.
SELECT 1;

-- +goose Down
-- Canonical Command kinds are not removed; binary rollback requires a backup.
SELECT 1;
