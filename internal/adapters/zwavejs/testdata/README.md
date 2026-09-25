# Z-Wave JS transcript fixtures

These JSONL files are synthetic, source-derived fixtures for the schema-29
Z-Wave JS WebSocket protocol. They are not household captures: no frame here
was recorded from a real Z-Wave network or a real installation.

Each file is a made-up but wire-shaped frame sequence for a small fictional
network:

- one `version` frame,
- the correlated `initialize` and `start_listening` result frames,
- zero or more `event` frames.

The Home ID, node names, locations, labels, and product fingerprints are
invented and intentionally sanitized. The frame shapes, field names, and enum
encodings mirror the documented schema-29 protocol so planning and translation
tests run against realistic wire input instead of hand-built Go values.

`switch-session.jsonl` covers Binary Switch endpoints, a propertyKeyed value, a
numeric property name, and a non-candidate property (`duration`), plus
ineligible nodes (controller, not ready, sleeping, half-interviewed, and
capability-free). `dimmer-session.jsonl` covers Multilevel Switch derived power,
labelled endpoints, and a level range that Hearth cannot mirror.

`loadTranscriptSnapshot` in `testhelp_test.go` is the only reader.
