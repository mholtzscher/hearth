# Control Hearth Devices from Apple Home

`hearth-adapter-homekit` exposes selected Hearth Devices to the Apple Home
app and Siri. It registers Adapter Functions but does not own Devices.
HomeKit writes use the same Core Commands as the dashboard.

The bridge's encrypted pairing, light control, and event delivery have been
checked with a software controller and simulated Devices. iPhone and home-hub
compatibility still require household testing.

## Run the bridge

1. Copy `configs/homekit.example.yaml` to a private `homekit.yaml`. Set
   `core_url` and `nats_url` to the existing Core and NATS endpoints.
2. Create a private directory for the bridge's identity. For example,
   `install -d -m 700 ./homekit-data`. Give the container user access to it.
   Use a different directory, Adapter ID, and TCP port for each bridge.
3. Mount the configuration read-only and the private directory read-write.
   Use host networking on Linux so the iPhone and home hub can discover
   the bridge and reach its advertised TCP port.

```yaml
services:
  homekit:
    image: ghcr.io/mholtzscher/hearth/hearth-adapter-homekit
    network_mode: host
    volumes:
      - ./homekit.yaml:/config/homekit.yaml:ro
      - ./homekit-data:/var/lib/hearth-adapter-homekit
    command: ["--config", "/config/homekit.yaml"]
```

The Adapter uses UDP 5353 for multicast DNS and TCP 21063 for HAP by default.
`interfaces` limits mDNS announcements to the named interfaces. If Apple Home
cannot add the bridge, check both discovery and TCP reachability. A visible
advertisement does not prove that the iPhone can reach the advertised address.
Do not forward either port from the internet. Keep Core HTTP and NATS on
loopback or a trusted private network.

Every YAML setting has a corresponding flag and `HEARTH_ADAPTER_HOMEKIT_`
environment variable. For example, `--listen-port` overrides
`HEARTH_ADAPTER_HOMEKIT_LISTEN_PORT`, which overrides `listen_port`.
See the [example configuration](../../configs/homekit.example.yaml).

## Choose Devices and pair

Ask the Hearth Agent to list HomeKit candidates, then explicitly expose the
Device you want. You can also invoke
[the same Functions over HTTP](../reference/adapter-functions.md#homekit-bridge).
The bridge does not expose a Device until you select it.

1. Invoke `expose_device` with the canonical Device ID. Use `list_accessories`
   to find the Device ID and review any unresolved Entity roles.
2. Invoke `pairing_info` while the bridge is unpaired. Treat both the setup code
   and the setup URI as credentials. Hearth retains the Function result in
   invocation history for 30 days, even after you pair. An Agent conversation
   that uses the Function can retain the result separately.
3. In Apple Home, choose **Add Accessory**. Select the discovered bridge and
   enter its setup code. Apple Home may show **Uncertified Accessory**; proceed
   only if you recognize the bridge you started.
4. To control the bridge away from home, configure an
   [Apple home hub](https://support.apple.com/en-us/102557) that can reach it.

Use `unexpose_device` to remove a Device. Exposure changes can briefly take the
whole bridge offline while HomeKit reloads. An accessory keeps its Hearth-owned
identifiers across reloads and restarts. If you remove and later re-expose it,
Apple Home may still drop its room, name, or automation assignments.

## Map ambiguous Entities

For a Device with several Entities of the same type, pass explicit assignments
in `expose_device`. For example, map the purifier's power and child-lock
Entities to `active` and `child_lock`, rather than guessing from their names.
For binary sensors, choose `contact`, `motion`, `occupancy`, or `leak`. Use
`inverted: true` when the upstream convention is opposite to the normal
mapping. For a contact that reports true when open, leave it unset.

The bridge supports lights, switches and outlets, locks, window coverings,
thermostats, air purifiers, and selected sensors. Only supported controls are
exposed. A garage cover uses WindowCovering; the bridge does not report
obstruction status or certify that the door is physically closed. A switch
exposed as an outlet derives `OutletInUse` from power state, not measured draw.

## Read faults and keep pairing data

The bridge rejects controls for an unreachable Entity and does not retry
Commands. It may acknowledge a HomeKit request before Core confirms a Command.
If a Command fails, use Hearth's Command history and current State to
determine what happened before trying again. A timeout does not prove that a
physical action did not happen. [Levoit](levoit.md#optimistic-control) can
report optimistic State that changes again on a later poll.

Back up the data directory as a unit while the bridge is stopped. It contains
pairing keys, setup credentials, and accessory identities. Restore it only into
one running bridge instance. The bridge never writes credentials to logs.

To start over, call `reset_pairing` with its exact confirmation string,
`reset_all_homekit_pairings`. This disconnects every paired controller and
creates new setup credentials. Apple Home treats the replacement as a new
bridge. Review locks in the exposure list before pairing it again.
