-- +goose Up
ALTER TABLE automation_history
ADD COLUMN fact_previous_value_json TEXT
CHECK (fact_previous_value_json IS NULL OR json_valid(fact_previous_value_json));

ALTER TABLE device_facts_outbox
ADD COLUMN previous_value_json TEXT
CHECK (previous_value_json IS NULL OR json_valid(previous_value_json));

-- +goose Down
ALTER TABLE device_facts_outbox DROP COLUMN previous_value_json;
ALTER TABLE automation_history DROP COLUMN fact_previous_value_json;
