# Connect Zigbee2MQTT to Hearth

This guide is for an operator connecting an existing Zigbee2MQTT installation
to Hearth and checking discovery without sending device Commands. Run commands
from the repository root.

## Prepare the services

Run Zigbee2MQTT, its coordinator, an MQTT broker, and NATS under your own
supervision. MQTT and NATS are separate services; the simulator does not start
an MQTT broker. The Adapter uses MQTT 3.1.1 over plain `mqtt://` or `tcp://`
with an explicit host and port. It has no MQTT authentication or TLS settings.
Keep MQTT, NATS, the Zigbee2MQTT management endpoint, and the unauthenticated
Hearth HTTP API on loopback or a trusted private network. An untrusted network
is unsupported. The Adapter's fixtures include captures from Zigbee2MQTT 2.13.0
and 2.14.1 as well as synthetic shapes, and its real-broker tests use the Mosquitto image pinned in
[`mosquittotest`](../../internal/platform/mosquitto/mosquittotest/mosquitto.go).
Other versions must provide compatible retained payloads and behavior.

Configure Zigbee2MQTT so its effective retained `bridge/info` reports:

```yaml
mqtt:
  version: 4
availability:
  enabled: true
device_options:
  optimistic: false
```

Each device `friendly_name` must be one valid UTF-8 MQTT topic level of
1–255 bytes, without `/`, `+`, `#`, or NUL, and cannot be `bridge`. Spaces and
non-ASCII characters are allowed. Use the Zigbee2MQTT `description` for a
different display name. The Adapter's `mqtt.base_topic` must be a subject-safe
slug, not a multi-level topic. See the
[friendly-name validator](../../internal/adapters/zigbee2mqtt/discovery.go)
and [configuration validation](../../internal/app/zigbee2mqtt/config.go).

## Configure and start

1. Copy the examples without overwriting existing configurations:

   ```sh
   cp -n configs/hearthd.example.yaml configs/hearthd.yaml
   cp -n configs/zigbee2mqtt.example.yaml configs/zigbee2mqtt.yaml
   mkdir -p .data
   ```

2. Set the same operator-managed NATS URL in both files. Set `mqtt.url` and
   `mqtt.base_topic` in `configs/zigbee2mqtt.yaml` to match Zigbee2MQTT.
   The loopback addresses are placeholders, not provisioned brokers.
3. Supply Core's selected agent-provider credential through your supervisor or
   secret manager. The example selects OpenAI and needs `HEARTH_OPENAI_API_KEY`;
   do not put its value in YAML, command arguments, or shell history.
4. Start the brokers and Zigbee2MQTT separately, then start Core and the Adapter
   in separate terminals or under a supervisor:

   ```sh
   go run ./cmd/hearthd --config configs/hearthd.yaml
   go run ./cmd/hearth-adapter-zigbee2mqtt --config configs/zigbee2mqtt.yaml
   ```

   Settings also accept flags and `HEARTH_ADAPTER_ZIGBEE2MQTT_` environment
   variables, which override YAML. Stop each foreground process with Ctrl-C, or
   use your supervisor. Stop the Adapter before Core.

## Discover and inspect

The default example uses `127.0.0.1:8080` and Adapter ID `zigbee2mqtt`. Adjust
these read-only requests if you changed either. Follow pagination when needed.

```sh
curl -fsS http://127.0.0.1:8080/v1/adapters/zigbee2mqtt
curl -fsS http://127.0.0.1:8080/v1/devices
curl -fsS http://127.0.0.1:8080/v1/entities
```

The Adapter stays unknown until it claims a session, subscribes, receives
retained `bridge/state`, `bridge/info`, and `bridge/devices`, and reconciles
registration. A compatible online bridge then becomes healthy. Entity
availability needs an explicit device availability message; State alone does
not establish availability. One IEEE address identifies one Device; endpoints
distinguish its Entity keys, not separate Devices. Friendly names are mutable
routes, not identity. Read the discovered Entity's support and State before
considering any separately authorized operation.

An observed Command becomes `satisfied` only after matching fresh, non-retained
evidence. Stateless effects and energy reset instead report `dispatched` after
MQTT acceptance, without proof of a physical effect. The Third Reality bulb
attribute/effect fixture is synthetic; its real startup values and effects are
not established by that fixture. MQTT QoS 1 can duplicate delivery. Hearth
does not automatically retry these effects; inspect Command history before
deciding whether to act again.

For an existing Command, replace the placeholders in these read-only requests:

```sh
curl -fsS 'http://127.0.0.1:8080/v1/commands/<command_id>'
curl -fsS 'http://127.0.0.1:8080/v1/entities/<entity_id>'
```

The `refresh_entity` Adapter Function sends a `/get` for an owned, gettable
Entity. Discover its current catalog revision before invoking it. Success means
MQTT PUBACK, not fresh State; do not automatically retry an unknown outcome.
See [Adapter Functions](../reference/adapter-functions.md) for invocation and
history, [architecture](../architecture/architecture.md) for supported
capabilities and evidence, and the
[capability guide](zigbee2mqtt-capabilities.md) for contributors.

## Troubleshoot

- For `hearth.external_system_unavailable`, check the broker listener, URL,
  and network path. For `bridge_offline`, check Zigbee2MQTT and its coordinator.
- For `incompatible_configuration`, check all three effective `bridge/info`
  settings above. For `invalid_inventory`, correct and republish the retained
  bridge information and device inventory.
- Missing devices may have invalid names, unsupported exposes, incomplete
  interviews, or be disabled. Inspect Adapter logs and Zigbee2MQTT metadata.
  Unknown availability requires an explicit availability message.
- A Command timeout means no matching fresh report arrived in time. Check
  whether the device answers `/get` and inspect its current State and Command
  history before considering another action.
