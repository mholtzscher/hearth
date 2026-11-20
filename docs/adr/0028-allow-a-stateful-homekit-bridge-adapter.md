---
status: accepted
---

# Allow a stateful HomeKit bridge Adapter

The HomeKit bridge is an Adapter so the Agent and operators can discover and
invoke its management Functions through Core. It registers no Devices. It uses
Core's HTTP API and the observation Device Fact stream as an ordinary client.
The Adapter SDK's ownership and read boundaries do not change.

The bridge owns the exposure list, HomeKit pairing identity and keys, and
accessory and characteristic ID allocations in a private, persistent data
directory. This is an exception to stateless first-party Adapters. None of that
storage becomes authoritative for Hearth State or Command outcomes. Back up and
restore the directory as a unit. Without it, operators may have to pair the
bridge again and restore Apple Home settings.

The bridge exposes only Devices explicitly selected through a Function. This
includes locks if selected. A paired controller can send a request to control an
exposed Device. The bridge translates requests into Core Commands; it does not
publish Observations or retry Commands after an uncertain outcome.

Keep Core HTTP and NATS on loopback or a trusted private network. HomeKit
pairing protects the HomeKit connection, not Core interfaces or Function
invocation history. Do not make Core HTTP or NATS reachable from the internet
to enable Apple home-hub access.
