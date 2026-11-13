# Levoit air purifier Adapter

`hearth-adapter-levoit` discovers every supported Core 400S and Core 600S on one
US VeSync account. It runs separately from Core through the public Hearth SDK,
without Home Assistant, a local database, or an inbound server.

This Adapter uses an unofficial cloud protocol. It needs outbound HTTPS to
`smartapi.vesync.com` and access to your trusted Hearth NATS listener. Internet
or VeSync outages interrupt readings and control. Other models, regions, MFA,
and Apple/Google-only login are unsupported. Do not disable account protection
to use the Adapter.

## Configure and run

1. Pair your purifiers in VeSync and verify ordinary email/password login works.
2. Create a private JSON file containing `email` and `password` string fields.
   Set permissions to `0600` or provide equivalent secret-volume access. Never
   put the values in YAML, command arguments, logs, or source control.
3. Copy `configs/levoit.example.yaml` to a private configuration file. Set
   `credentials_file` to that file's path, choose a stable unique `adapter_id`,
   and set `nats_url` to your Hearth NATS endpoint.
4. Start the companion while Core is running:

```sh
go run ./cmd/hearth-adapter-levoit --config /path/to/levoit.yaml --log-format json
```

The release image is `ghcr.io/mholtzscher/hearth/hearth-adapter-levoit`.
Mount config and credentials read-only. Restart applies credentials changes.
Session tokens remain in memory and are never written to disk.

| Configuration | Meaning |
| --- | --- |
| `adapter_id` | Stable subject-safe Adapter instance slug |
| `nats_url` | NATS URL; CLI default `nats://127.0.0.1:4222` |
| `credentials_file` | Required regular JSON file, at most 16 KiB |
| `poll_interval` | Default `60s`, range `30s`–`1h` |
| `time_zone` | IANA timezone sent as protocol metadata; default `UTC` |

All fields have matching hyphenated flags and `HEARTH_ADAPTER_LEVOIT_`
environment names, such as `--credentials-file` and
`HEARTH_ADAPTER_LEVOIT_CREDENTIALS_FILE`. Flags override environment, then YAML.
Use `--config` or `HEARTH_ADAPTER_LEVOIT_CONFIG` to select a YAML file; the implicit
default is `configs/levoit.yaml`. Logging uses `--log-level` and `--log-format`
or the corresponding environment settings, never YAML. Credential values have
no flags or environment settings.

## Read and control the purifiers

Each purifier is one Device of kind `air-purifier`, containing these Entities:

| Entity key | Value | Control |
| --- | --- | --- |
| `power` | Boolean enabled flag | Boolean `set` |
| `mode` | `manual`, `auto`, `sleep` | String `set` |
| `fan-level` | Stored manual selection `1`–`4`, as a string | String `set` |
| `display` | Configured screen enabled flag | Boolean `set` |
| `child-lock` | Boolean child lock | Boolean `set` |
| `pm25` | PM2.5 concentration, `µg/m³` | Read-only |
| `filter-life` | Remaining filter life, percent | Read-only |

Use the existing Entity APIs, MCP tools, history, and automations. For example,
`power.set` takes `{"value":true}` and `fan-level.set` takes `{"value":"3"}`.
There are no Adapter Functions. Dashboards show and control a purifier with the
AirPurifier card or tile; see [dashboards](dashboards.md).

Upgrade Core before the Levoit Adapter. An upgraded Adapter declaring `µg/m³`
against an unmigrated Core is rejected with `immutable_interpretation_change`
and exits. Rolling the Adapter back after Core migration 33 fails registration
the same way; restore a pre-upgrade backup with matching binaries instead.

Setting a fan level selects manual mode. Selecting `manual` explicitly starts
at level 1. Mode and fan-level Commands optimistically report power on as well.
`fan-level` is the
stored manual selection, not measured fan speed in auto/sleep mode. If the
provider does not report that selection, the Entity becomes unavailable instead
of inventing a value. `display` reports the configured setting, not whether the
screen is temporarily illuminated.

### Optimistic control

A Command sends one mutation. After VeSync acknowledges success, the Adapter
publishes the requested control value as a linked optimistic Observation. Core
records the Command as `satisfied` without a confirmation read. For this Adapter,
success means cloud acceptance and an optimistic State update, not device
confirmation. Acknowledgements with zero outer and inner codes are valid even
when they contain no result object.

Optimistic control values enter canonical State, history, and automation triggers
just like cloud readings. The existing APIs do not distinguish their source.
Only the requested control and its known side effects update: manual mode also
reports level 1 and power on; fan level also reports manual mode and power on;
auto/sleep mode reports power on. Other controls and sensor availability remain
untouched. PM2.5 and filter life always come from status reads.

Periodic polls replace optimistic State, even when VeSync returns a stale
snapshot. A requested power-off can display off, roll back to on at the next
poll, and return to off when the cloud catches up. There is no holdoff or desired
State cache. A live Core 400S check showed power status lagging by about three
minutes. A later rollback does not rewrite the retained satisfied Command.

The ten-second Command deadline is unchanged. Check retained outcomes before
deciding to send another Command. The Adapter never automatically resends a
mutation, queues it for recovery, or replays it after restart.

If a mutation's response is lost or malformed, the Adapter has no authoritative
accept/reject evidence. Core retains its existing `adapter_unhealthy` Command
transport-failure outcome, not a definite effect rejection. That outcome does
not prove whether the purifier changed; no mutation is replayed.

## Discovery and recovery

Acquisition runs immediately and waits one interval after each completed cycle.
Each cycle completes paginated discovery, then reads online supported Devices.
With two online purifiers and the default interval, the scheduled request count
is about 4320 per day, before extra inventory pages, login, and Commands. This
is not an official quota guarantee.

Device identity derives from its VeSync CID, never its name. Renaming preserves
Hearth IDs, enablement, and name overrides. Replacing hardware with a new CID
creates a new Device. Removing a purifier from the account marks its Entities
unavailable and removes its command routes; it does not delete history or disable
Entities. Re-adding the same CID restores its identity. A failed or partial
inventory does not prove removal.

A complete inventory establishes cloud access and Adapter health. An offline
purifier or failed status read affects its Entities without making another
purifier unavailable. Authentication or inventory failure makes Adapter health
unhealthy. Missing and malformed fields make their individual Entities unavailable;
valid siblings still update. Last-known State remains readable through outages.

| Safe error class | Action |
| --- | --- |
| `authentication_rejected` | Check the private credentials file and restart |
| `unsupported_authentication` | Check account region/login requirements; MFA is not implemented |
| `session_expired` | Scheduled acquisition can renew the session; a failed Command is not replayed |
| `provider_throttled` | Wait for shared cooldown; do not repeatedly send Commands |
| `provider_unavailable` | Check connectivity; later cycles reassess |
| `invalid_response` | Cloud protocol or firmware may have changed; preserve sanitized evidence |
| `device_offline`, `device_missing` | Check power, Wi-Fi, and account membership |
| `measurement_unavailable` | Provider omitted or supplied an invalid field; last State is retained |

HTTP attempts have five-second deadlines and a 1 MiB decoded body limit.
Redirects are refused. Rate limiting applies a shared cooldown to new cloud
requests. Authentication rejection requires restart to avoid repeatedly submitting
bad credentials. Shutdown cancels HTTP, closes Command admission, and joins work
before releasing the SDK Session.

## Verify compatibility

Run focused local checks through repository tasks:

```sh
GO_PACKAGES='./internal/adapters/levoit ./internal/app/levoit ./cmd/hearth-adapter-levoit' mise run test -- -tags=integration,e2e
```

Synthetic fixtures establish protocol handling and the Core journey, not actual
VeSync account or firmware compatibility. For a live check, provide credentials
through the private file and run the companion. Discover both Devices through
`GET /v1/devices`, then inspect their seven Entities through `GET /v1/entities`.
Compare PM2.5, filter life, mode, and settings with VeSync.

With the household's permission, record current settings, exercise each control
on both models, inspect retained Command outcomes and optimistic State, and restore
the original settings. Confirm restoration separately from later cloud reads;
optimistic success alone is not restoration evidence. Verify offline/recovery
behavior and unchanged IDs after
restart. Record model, firmware, and sanitized outcomes. Never record credentials,
tokens, raw auth traffic, or account identifiers. Unrun live checks remain a
compatibility limitation.
