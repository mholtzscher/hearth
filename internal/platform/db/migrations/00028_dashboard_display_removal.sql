-- Display units became a household setting (household_unit_system), so saved
-- Metric and TimeSeries cards no longer store props.display. Up removes it;
-- Down restores the shapes migration 27's definitions used: celsius on
-- temperature cards and native on every other reading type. Revision and
-- updated_at stay unchanged: the rewrite preserves what each card reads.

-- +goose Up
WITH RECURSIVE
targets AS (
    SELECT d.id AS dashboard_id,
           '$.spec.elements.' || json_quote(e.key) || '.props.display' AS path,
           row_number() OVER (PARTITION BY d.id ORDER BY e.key) AS n
    FROM dashboards AS d, json_each(d.definition_json, '$.spec.elements') AS e
    WHERE json_valid(d.definition_json)
      AND json_type(d.definition_json, '$.spec.elements') = 'object'
      AND json_extract(e.value, '$.type') IN ('Metric', 'TimeSeries')
      AND json_type(e.value, '$.props.display') IS NOT NULL
),
applied(dashboard_id, n, doc) AS (
    SELECT id, 0, definition_json FROM dashboards WHERE id IN (SELECT dashboard_id FROM targets)
    UNION ALL
    SELECT a.dashboard_id, a.n + 1, json_remove(a.doc, t.path)
    FROM applied AS a
    JOIN targets AS t ON t.dashboard_id = a.dashboard_id AND t.n = a.n + 1
)
UPDATE dashboards
SET definition_json = (
    SELECT doc FROM applied WHERE applied.dashboard_id = dashboards.id ORDER BY n DESC LIMIT 1
)
WHERE id IN (SELECT dashboard_id FROM targets);

-- +goose Down
WITH RECURSIVE
targets AS (
    SELECT d.id AS dashboard_id,
           '$.spec.elements.' || json_quote(e.key) || '.props.display' AS path,
           CASE coalesce(json_extract(e.value, '$.props.source.entity_type'),
                         json_extract(e.value, '$.props.series[0].source.entity_type'))
               WHEN 'hearth.temperature/v1' THEN 'celsius'
               ELSE 'native'
           END AS unit,
           row_number() OVER (PARTITION BY d.id ORDER BY e.key) AS n
    FROM dashboards AS d, json_each(d.definition_json, '$.spec.elements') AS e
    WHERE json_valid(d.definition_json)
      AND json_type(d.definition_json, '$.spec.elements') = 'object'
      AND json_extract(e.value, '$.type') IN ('Metric', 'TimeSeries')
      AND json_type(e.value, '$.props.display') IS NULL
),
applied(dashboard_id, n, doc) AS (
    SELECT id, 0, definition_json FROM dashboards WHERE id IN (SELECT dashboard_id FROM targets)
    UNION ALL
    SELECT a.dashboard_id, a.n + 1, json_set(a.doc, t.path, json_object('unit', t.unit))
    FROM applied AS a
    JOIN targets AS t ON t.dashboard_id = a.dashboard_id AND t.n = a.n + 1
)
UPDATE dashboards
SET definition_json = (
    SELECT doc FROM applied WHERE applied.dashboard_id = dashboards.id ORDER BY n DESC LIMIT 1
)
WHERE id IN (SELECT dashboard_id FROM targets);
