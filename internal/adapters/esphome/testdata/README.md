# ESPHome Adapter fixtures

These fixtures are synthetic and source-derived. They are not captures from a
real device.

- `apollo-temp1.json` and `apollo-air1.json` list the entities that Apollo
  Automation's TEMP-1 and AIR-1 firmware (`Integrations/ESPHome/Core.yaml` on
  `main`, November 2026) expose through the ESPHome Native API. Entity
  categories, device classes, units, and `disabled_by_default` come from that
  YAML. Where the YAML sets none, they come from ESPHome 2026.10.2 platform
  defaults: `uptime`, `internal_temperature`, `wifi_signal`, and the `status`
  binary sensor are diagnostic, and the `restart` and `factory_reset` buttons
  are configuration. Internal entities, such as the `homeassistant` OTA Mode
  binary sensor, are omitted.
- Keys are arbitrary. MAC addresses use the locally administered
  `02:00:00:00:00:xx` range. State values are plausible readings, not household
  data.

The fixtures prove the planner's contract against these entity lists. They do
not prove compatibility with real firmware.
