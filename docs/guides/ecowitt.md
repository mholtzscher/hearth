# Connect an Ecowitt station to Hearth

This guide is for an operator connecting a GW2000 gateway and WS90 outdoor
array through Customized Server MQTT and checking read-only measurements.
Run commands from the repository root.

## Prepare the network and gateway

Run NATS and an MQTT broker separately under your own supervision. The gateway
must reach the MQTT listener over the network: a loopback-only listener on
another host cannot receive its uploads. Bind the listener to an approved
private address and restrict access to the trusted private network.

The Adapter uses plain MQTT 3.1.1, with no username, password, TLS, or
WebSocket. It never publishes to MQTT. Keep the gateway, broker, Adapter, NATS,
and unauthenticated Hearth HTTP API inside the trusted boundary; untrusted
network exposure is unsupported. The PASSKEY prevents accidental cross-station
ingestion; it does not secure the MQTT connection.

In the GW2000 **Customized Server** settings, enable MQTT and select the
private broker host and port. Set the topic exactly to `mqtt.topic` and the
upload interval exactly to `station.upload_interval_seconds` in the Adapter
configuration. The configured topic has exactly two segments. The example
topic and its address are placeholders.

The Adapter targets a `GW2000`-prefixed station and a WS90 array. The
[sanitized captured report](../../internal/adapters/ecowitt/testdata/gw2000-ws90-report.txt)
preserves representative field names and shapes from a GW2000B V3.3.2 report;
the [parser test](../../internal/adapters/ecowitt/report_test.go) identifies
its captured firmware provenance. Other firmware is compatible only if its
reports retain the required field semantics. A captured report is not proof
that every firmware or sensor works.

## Configure and start

1. Copy the examples without overwriting existing configurations:

   ```sh
   cp -n configs/hearthd.example.yaml configs/hearthd.yaml
   cp -n configs/ecowitt.example.yaml configs/ecowitt.yaml
   mkdir -p .data
   ```

2. Store the gateway's 32-character hexadecimal PASSKEY in a separate private
   file readable by the Adapter process. Compatible reports include
   `PASSKEY=<value>`. To find it, start the broker and gateway (step 5) and
   read one report from the gateway's topic, for example with
   `mosquitto_sub -h <broker-host> -p <broker-port> -t <topic> -C 1`. Use your secret manager or a private
   editor, not shell history, flags, environment variables, or YAML values.
   Set `station.passkey_file` to its path. The Adapter reads it at startup;
   restart after a change.
3. Set the operator-managed NATS URL in both files. Set `mqtt.url` to the
   broker's explicit host and port and set the topic and upload interval to
   match the gateway. The Adapter accepts `mqtt://` and `tcp://` URLs.
4. Supply Core's selected agent-provider credential through your supervisor or
   secret manager. The example selects OpenAI and needs `HEARTH_OPENAI_API_KEY`.
   Keep the value out of YAML, command arguments, and shell history.
5. Start the brokers and gateway under their own supervision. Start Core and
   the Adapter in separate terminals or under a supervisor:

   ```sh
   go run ./cmd/hearthd --config configs/hearthd.yaml
   go run ./cmd/hearth-adapter-ecowitt --config configs/ecowitt.yaml
   ```

   Flags and `HEARTH_ADAPTER_ECOWITT_` environment settings override YAML;
   use them for non-secret settings only. Stop foreground processes with Ctrl-C
   or use your supervisor. Stop the Adapter before Core.

## Inspect measurements

The example uses Core at `127.0.0.1:8080` and Adapter ID `ecowitt`. Adjust
these read-only requests for your deployment and follow pagination as needed:

```sh
curl -fsS http://127.0.0.1:8080/v1/adapters/ecowitt
curl -fsS http://127.0.0.1:8080/v1/devices
curl -fsS http://127.0.0.1:8080/v1/entities
```

The Adapter registers `gateway` and `outdoor-array` slots before connecting to
MQTT. Their Entities are read-only. Health becomes healthy only after a live,
non-retained, non-duplicate, valid compatible report on the configured topic.
A valid measurement establishes that Entity's availability. A measurement
that is absent or invalid for three configured upload intervals becomes
unavailable. A never-observed Entity remains unknown until its first valid
measurement or three intervals after the first accepted report. State alone
does not prove current availability.

Identity belongs to the configured slot, not replaceable radio hardware.
Replacing hardware in a slot preserves canonical IDs and therefore asserts
that it represents the same household station. Use a different `adapter_id`
for a separately meaningful station. See [architecture](../architecture/architecture.md)
and the [Entity schemas](../../entitytypes/) for measurements and units.

## Troubleshoot

- For `hearth.external_system_unavailable`, check the broker URL, private
  listener, and network path from the Adapter.
- For `station_silent`, check gateway power, broker reachability, Customized
  Server topic, and matching upload cadence. Verify the PASSKEY file matches
  without exposing its contents.
- For `measurement_stale` or an unknown Entity, inspect the gateway's sensor
  and firmware. Retained, wrong-topic, wrong-PASSKEY, incompatible, malformed,
  or duplicate reports cannot establish fresh evidence. One invalid measurement
  does not invalidate an otherwise valid sibling.
- Avoid collecting raw payloads or the station topic in public diagnostics:
  they can contain the PASSKEY or station MAC.
