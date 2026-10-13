-- +goose Up
-- Persisted v1 Command Steps omitted kind. Upgrade only those Steps, including
-- nested branch sequences and immutable Run snapshots; public v2 input remains
-- strict. Do not change revisions, timestamps, ordering, or execution evidence.
CREATE TEMP TABLE automation_command_kind_upgrades AS
WITH RECURSIVE
documents(source, id, document) AS (
    SELECT 'definition', id, definition_json FROM automations
    UNION ALL
    SELECT 'snapshot', id, run_snapshot_json FROM automation_history
    WHERE run_snapshot_json IS NOT NULL
),
sequences(source, id, path, steps) AS (
    SELECT source, id, '$.steps', json_extract(document, '$.steps')
    FROM documents WHERE json_type(document, '$.steps') = 'array'
    UNION ALL
    SELECT sequences.source, sequences.id,
        sequences.path || '[' || step.key || '].' || child.key, child.value
    FROM sequences, json_each(sequences.steps) AS step, json_each(step.value) AS child
    WHERE json_extract(step.value, '$.kind') IN ('if', 'choose')
        AND child.key IN ('then', 'else', 'default') AND child.type = 'array'
    UNION ALL
    SELECT sequences.source, sequences.id,
        sequences.path || '[' || step.key || '].branches[' || branch.key || '].steps',
        json_extract(branch.value, '$.steps')
    FROM sequences, json_each(sequences.steps) AS step,
        json_each(step.value, '$.branches') AS branch
    WHERE json_extract(step.value, '$.kind') = 'choose'
        AND json_type(branch.value, '$.steps') = 'array'
),
changes(source, id, path, ordinal) AS (
    SELECT sequences.source, sequences.id, sequences.path || '[' || step.key || '].kind',
        row_number() OVER (PARTITION BY sequences.source, sequences.id ORDER BY sequences.path, step.key)
    FROM sequences, json_each(sequences.steps) AS step
    WHERE step.type = 'object' AND json_type(step.value, '$.kind') IS NULL
        AND json_type(step.value, '$.entity_id') = 'text'
        AND json_type(step.value, '$.operation') = 'text'
        AND json_type(step.value, '$.parameters') = 'object'
),
rewrites(source, id, ordinal, document) AS (
    SELECT source, id, 0, document FROM documents
    WHERE EXISTS (SELECT 1 FROM changes WHERE changes.source = documents.source AND changes.id = documents.id)
    UNION ALL
    SELECT rewrites.source, rewrites.id, changes.ordinal,
        json_insert(rewrites.document, changes.path, 'command')
    FROM rewrites JOIN changes ON changes.source = rewrites.source AND changes.id = rewrites.id
        AND changes.ordinal = rewrites.ordinal + 1
)
SELECT source, id, document FROM rewrites
WHERE NOT EXISTS (SELECT 1 FROM changes WHERE changes.source = rewrites.source AND changes.id = rewrites.id
    AND changes.ordinal = rewrites.ordinal + 1);

UPDATE automations SET definition_json = (
    SELECT document FROM automation_command_kind_upgrades WHERE source = 'definition' AND id = automations.id
)
WHERE id IN (SELECT id FROM automation_command_kind_upgrades WHERE source = 'definition');

UPDATE automation_history SET run_snapshot_json = (
    SELECT document FROM automation_command_kind_upgrades WHERE source = 'snapshot' AND id = automation_history.id
)
WHERE id IN (SELECT id FROM automation_command_kind_upgrades WHERE source = 'snapshot');

DROP TABLE automation_command_kind_upgrades;

-- +goose Down
-- Representation-only upgrade: retain explicit kinds. Removing them would
-- corrupt v2 documents; binary rollback requires a pre-upgrade backup.
