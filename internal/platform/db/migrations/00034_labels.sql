-- +goose Up
CREATE TABLE labels (
    id TEXT PRIMARY KEY CHECK (substr(id, 1, 4) = 'lbl_'),
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    -- Uniqueness key derived by devices.NormalizeLabelName; unique across kinds.
    name_key TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('area', 'tag')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- The write transaction checks that an assigned Area names an Area Label.
ALTER TABLE devices ADD COLUMN area_id TEXT REFERENCES labels(id) ON DELETE SET NULL;
ALTER TABLE entities ADD COLUMN area_id TEXT REFERENCES labels(id) ON DELETE SET NULL;

CREATE TABLE device_tags (
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    label_id TEXT NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    PRIMARY KEY (device_id, label_id)
);
CREATE INDEX device_tags_label_idx ON device_tags(label_id);

CREATE TABLE entity_tags (
    entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    label_id TEXT NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    PRIMARY KEY (entity_id, label_id)
);
CREATE INDEX entity_tags_label_idx ON entity_tags(label_id);

-- The only definition of effective Labels. An Entity's Area is its own, else
-- its Device's. Its tags are the union of its own and its Device's; a tag held
-- by both appears once per source.
CREATE VIEW entity_effective_labels AS
SELECT e.id AS entity_id,
    CAST(COALESCE(e.area_id, d.area_id) AS TEXT) AS label_id,
    'area' AS kind,
    CASE WHEN e.area_id IS NOT NULL THEN 'entity' ELSE 'device' END AS source
FROM entities AS e
JOIN devices AS d ON d.id = e.device_id
WHERE COALESCE(e.area_id, d.area_id) IS NOT NULL
UNION ALL
SELECT et.entity_id, et.label_id, 'tag', 'entity'
FROM entity_tags AS et
UNION ALL
SELECT e.id, dt.label_id, 'tag', 'device'
FROM entities AS e
JOIN device_tags AS dt ON dt.device_id = e.device_id;

-- +goose Down
-- Downgrade discards Labels and their assignments. Back up the database first.
DROP VIEW entity_effective_labels;
DROP TABLE entity_tags;
DROP TABLE device_tags;
ALTER TABLE entities DROP COLUMN area_id;
ALTER TABLE devices DROP COLUMN area_id;
DROP TABLE labels;
