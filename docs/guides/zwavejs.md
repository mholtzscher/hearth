# Connect Z-Wave JS to Hearth

This guide is for an operator connecting an existing Z-Wave JS network to
Hearth and inspecting discovery without sending device Commands. Run commands
from the repository root.

## Protect the existing network

Keep the USB controller, network cache, and S0/S2 keys with the operator-managed
Z-Wave JS UI service. Enable its embedded Z-Wave JS server. The Adapter never
reads the keys or performs network management, including inclusion, exclusion,
interviews, healing, configuration, and firmware updates.

The Adapter uses server API schema 29. The server's advertised schema range
must include 29, and the Home ID in its version frame must match the snapshot.
The Adapter accepts an absolute lowercase `ws://` URL with explicit host and
port, no credentials, query, or fragment, and only an empty or root path.
The server has no authentication or TLS, and `wss://` is unsupported. Keep its
listener, the Adapter, NATS, and the unauthenticated Hearth HTTP API on loopback
or a trusted private network. Untrusted-network exposure is unsupported.

## Configure and start

1. Copy the example without overwriting an existing configuration:

   ```sh
   cp -n configs/zwavejs.example.yaml configs/zwavejs.yaml
   ```

2. Set `zwave_js.url` to the existing server's trusted `ws://host:port`
   endpoint. Keep the example `nats_url` for the managed task. Disabling
   optimistic value updates in Z-Wave JS UI is recommended, but command
   satisfaction does not depend on it.
3. Supply the selected agent-provider credential through your supervisor or
   secret manager. The simulator's checked-in Core selects OpenCode Go and
   requires `HEARTH_OPENCODE_GO_API_KEY`, not the normal Core example's OpenAI
   key. If you change the provider, supply that provider's credential instead.
   Keep its value out of YAML, arguments, and shell history.
4. Start the optional Adapter and inspect its status and worktree ports:

   ```sh
   mise run zwave-start
   mise daemons status zwave-adapter
   mise env --json | jq '{SIM_CORE_PORT, SIM_WEB_PORT}'
   ```

The task starts the simulator stack if needed and substitutes its assigned NATS
port in an ignored runtime copy under `.data/simulator-stack/`. It shares Core,
NATS, the database, and dashboard with simulated Devices. Their records and
physical Device records remain after stopping. Discovery sends no device
Commands. To stop the Adapter, then the remaining simulator stack:

```sh
mise run zwave-stop
mise run simulator-stop
```

## Inspect discovery and identity

Replace `<SIM_CORE_PORT>` with the printed port in these read-only requests.
Lists return at most one page; when a response has `next_cursor`, repeat the request with `?cursor=<next_cursor>`:

```sh
curl -fsS 'http://127.0.0.1:<SIM_CORE_PORT>/v1/adapters/zwavejs'
curl -fsS 'http://127.0.0.1:<SIM_CORE_PORT>/v1/devices'
curl -fsS 'http://127.0.0.1:<SIM_CORE_PORT>/v1/entities'
```

The Adapter remains unknown until it negotiates schema 29, validates Home ID,
and reconciles a complete snapshot. A valid empty network can be healthy.
Discovery registers ready, completely interviewed, always-listening,
non-controller nodes with eligible Binary Switch or Multilevel Switch value
pairs. Sleeping and frequently-listening actuators are excluded. Inspect
Entity support and explicit availability before any separately authorized
operation.

Identity is the Home ID and node ID network slot, not immutable hardware
identity. Re-inclusion with a new node ID creates a new Device and leaves the
old one unavailable. A controller can reuse a removed node ID and thus reuse
the old Hearth identity. **Disable the node's Hearth Entities before removing
it**; safe slot reuse requires reconciliation support that is not yet present.
To disable one Entity, send
`PATCH /v1/entities/<entity_id>` with `Content-Type: application/merge-patch+json`
and body `{"enabled":false}`. Repeat for each Entity that
`GET /v1/entities?device_id=<device_id>` lists for the node.
If owned mappings have another Home ID, the Adapter registers nothing and
reports `network_identity_mismatch`. Restore the intended endpoint or use a
new `adapter_id` for a different network.

## Understand Command evidence

Power and brightness Commands are accepted only after a recognized successful
`node.set_value` result. Satisfaction requires a fresh correlated
`node.poll_value` report; a post-set update event can be optimistic and is not
linked evidence. A timed-out write may still complete later because Z-Wave JS
cannot cancel work already handed to the driver. Re-read State and Command
history before deciding what to do; do not assume a timeout prevented a
physical effect. Brightness requests use percentages; native 0–99 level
conversion means a requested 50% can report 51% and still satisfy the Command.

To inspect an existing Command and Entity, replace the placeholders:

```sh
curl -fsS 'http://127.0.0.1:<SIM_CORE_PORT>/v1/commands/<command_id>'
curl -fsS 'http://127.0.0.1:<SIM_CORE_PORT>/v1/entities/<entity_id>'
```

Read-only discovery and State projection have been smoke-tested against a live
Home Assistant Z-Wave JS app. Physical Commands remain unvalidated; the
[wire fixtures](../../internal/adapters/zwavejs/testdata/README.md) are
synthetic and do not prove physical compatibility. The
[open hardware acceptance spec](../../specs/zwave-js-adapter.md#acceptance-criteria)
describes the missing sanitized evidence and explicitly approved switch and
dimmer trials. Do not perform those trials without authorization.

## Troubleshoot

- For `hearth.external_system_unavailable`, check the server listener, URL,
  and network path. For `incompatible_protocol`, check that the server's schema
  range includes 29. For `invalid_snapshot`, inspect the Z-Wave JS driver logs.
- For `network_identity_mismatch`, check the controller and Adapter ID before
  changing any mappings. Do not silently reuse an existing network identity.
- A missing or unavailable node may be asleep, dead, not ready, incompletely
  interviewed, or missing an eligible value pair. Inspect Adapter logs and
  the server's node status.
- After a Command timeout, inspect the node's `currentValue`, its reachability,
  and the retained Command outcome. A timeout does not cancel radio work.
