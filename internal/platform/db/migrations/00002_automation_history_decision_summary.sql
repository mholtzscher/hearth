-- +goose Up
-- Automation history decision summary columns. The listing projection needs
-- only the decision mode, derived bypass flag, and evaluated root result; with
-- dedicated columns it no longer parses the full condition_decision_json
-- document (snapshot and evidence included) for every list-page row. The
-- columns are derived from the decision document and backfilled for rows
-- recorded before this migration.

ALTER TABLE automation_history ADD COLUMN condition_mode TEXT NOT NULL DEFAULT 'not_configured'
    CHECK (condition_mode IN ('not_configured', 'not_evaluated', 'bypassed', 'evaluated'));

ALTER TABLE automation_history ADD COLUMN condition_bypassed INTEGER NOT NULL DEFAULT 0
    CHECK (condition_bypassed IN (0, 1));

ALTER TABLE automation_history ADD COLUMN condition_result TEXT
    CHECK (condition_result IS NULL OR condition_result IN ('true', 'false', 'unknown'));

UPDATE automation_history
SET
    condition_mode = json_extract(condition_decision_json, '$.mode'),
    condition_bypassed = CASE
        WHEN json_extract(condition_decision_json, '$.mode') = 'bypassed' THEN 1
        ELSE 0
    END,
    condition_result = CASE
        WHEN json_extract(condition_decision_json, '$.mode') = 'evaluated'
        THEN json_extract(condition_decision_json, '$.evaluation.result')
    END;

-- +goose Down
UPDATE automation_history SET condition_mode = 'not_configured';
UPDATE automation_history SET condition_bypassed = 0;
UPDATE automation_history SET condition_result = NULL;
ALTER TABLE automation_history DROP COLUMN condition_mode;
ALTER TABLE automation_history DROP COLUMN condition_bypassed;
ALTER TABLE automation_history DROP COLUMN condition_result;
