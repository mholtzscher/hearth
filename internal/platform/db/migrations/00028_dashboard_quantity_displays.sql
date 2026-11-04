-- Saved dashboards stored "native" displays for every non-temperature numeric
-- card. Fixed-unit quantities now name their display (percent, hpa, mps); only
-- per-Entity-unit readings (numericsensor) keep "native". Type IDs are frozen
-- here because migrations must not depend on the generated readings catalog.
-- Revision and updated_at stay unchanged: the rewrite preserves meaning.

-- +goose Up
WITH RECURSIVE
targets AS (
    SELECT dashboard_id, path, unit,
           row_number() OVER (PARTITION BY dashboard_id ORDER BY path) AS n
    FROM (
        SELECT d.id AS dashboard_id,
               '$.spec.elements.' || json_quote(e.key) || '.props.display.unit' AS path,
               CASE coalesce(json_extract(e.value, '$.props.source.entity_type'),
                             json_extract(e.value, '$.props.series[0].source.entity_type'))
                   WHEN 'hearth.relativehumidity/v1' THEN 'percent'
                   WHEN 'hearth.brightness/v1' THEN 'percent'
                   WHEN 'hearth.pressure/v1' THEN 'hpa'
                   WHEN 'hearth.speed/v1' THEN 'mps'
               END AS unit
        FROM dashboards AS d, json_each(d.definition_json, '$.spec.elements') AS e
        WHERE json_valid(d.definition_json)
          AND json_type(d.definition_json, '$.spec.elements') = 'object'
          AND json_extract(e.value, '$.props.display.unit') = 'native'
    )
    WHERE unit IS NOT NULL
),
applied(dashboard_id, n, doc) AS (
    SELECT id, 0, definition_json FROM dashboards WHERE id IN (SELECT dashboard_id FROM targets)
    UNION ALL
    SELECT a.dashboard_id, a.n + 1, json_set(a.doc, t.path, t.unit)
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
    SELECT dashboard_id, path,
           row_number() OVER (PARTITION BY dashboard_id ORDER BY path) AS n
    FROM (
        SELECT d.id AS dashboard_id,
               '$.spec.elements.' || json_quote(e.key) || '.props.display.unit' AS path,
               coalesce(json_extract(e.value, '$.props.source.entity_type'),
                        json_extract(e.value, '$.props.series[0].source.entity_type')) AS entity_type,
               json_extract(e.value, '$.props.display.unit') AS unit
        FROM dashboards AS d, json_each(d.definition_json, '$.spec.elements') AS e
        WHERE json_valid(d.definition_json)
          AND json_type(d.definition_json, '$.spec.elements') = 'object'
    )
    WHERE (entity_type IN ('hearth.relativehumidity/v1', 'hearth.brightness/v1') AND unit = 'percent')
       OR (entity_type = 'hearth.pressure/v1' AND unit = 'hpa')
       OR (entity_type = 'hearth.speed/v1' AND unit = 'mps')
),
applied(dashboard_id, n, doc) AS (
    SELECT id, 0, definition_json FROM dashboards WHERE id IN (SELECT dashboard_id FROM targets)
    UNION ALL
    SELECT a.dashboard_id, a.n + 1, json_set(a.doc, t.path, 'native')
    FROM applied AS a
    JOIN targets AS t ON t.dashboard_id = a.dashboard_id AND t.n = a.n + 1
)
UPDATE dashboards
SET definition_json = (
    SELECT doc FROM applied WHERE applied.dashboard_id = dashboards.id ORDER BY n DESC LIMIT 1
)
WHERE id IN (SELECT dashboard_id FROM targets);
