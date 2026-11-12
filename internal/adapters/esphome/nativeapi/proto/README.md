# Vendored ESPHome Native API schema

`api.proto` and `api_options.proto` are unmodified copies of
`esphome/components/api/` at ESPHome tag `2026.10.2` (API version 1.19).

To move to a newer ESPHome release, replace both files with the new tag's
copies, update the tag and API version here and in `../client.go`
(`clientAPIMinor`), run `mise run --skip-deps generate`, and rerun the
`nativeapi` and `esphome` adapter tests.

The schema files are MIT-licensed under ESPHome's policy for non-C++ files;
see the adjacent `LICENSE` and the upstream
[license policy](https://github.com/esphome/esphome/blob/2026.10.2/LICENSE).
