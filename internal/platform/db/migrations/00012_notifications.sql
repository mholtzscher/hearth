-- +goose Up
CREATE TABLE notification_destinations (
 id TEXT PRIMARY KEY,
 adapter_id TEXT NOT NULL,
 local_key TEXT NOT NULL,
 runtime_id TEXT NOT NULL,
 name TEXT NOT NULL,
 UNIQUE(adapter_id, local_key)
);
CREATE TABLE notification_attempts (
 id TEXT PRIMARY KEY,
 destination_id TEXT NOT NULL,
 destination_name TEXT NOT NULL,
 adapter_id TEXT NOT NULL,
 local_key TEXT NOT NULL,
 runtime_id TEXT NOT NULL,
 title TEXT NOT NULL,
 message TEXT NOT NULL,
 started_at TEXT NOT NULL,
 deadline TEXT NOT NULL,
 completed_at TEXT,
 outcome TEXT NOT NULL DEFAULT '',
 reason_code TEXT NOT NULL DEFAULT '',
 receipt TEXT NOT NULL DEFAULT '',
 CHECK ((outcome = '' AND completed_at IS NULL AND reason_code = '' AND receipt = '') OR
        (outcome IN ('accepted','rejected','not_sent','unknown') AND completed_at IS NOT NULL)),
 CHECK ((outcome = 'accepted' AND reason_code = '') OR (outcome != 'accepted' AND receipt = ''))
);
CREATE INDEX notification_attempts_history ON notification_attempts(started_at DESC,id DESC);
CREATE INDEX notification_attempts_destination_history ON notification_attempts(destination_id,started_at DESC,id DESC);
-- +goose Down
DROP TABLE notification_attempts;
DROP TABLE notification_destinations;
