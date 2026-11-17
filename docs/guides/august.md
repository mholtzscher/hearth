# August cloud monitoring companion

The August companion is a native Go, read-only monitor for explicitly selected US
August locks. It uses the unofficial fixed `https://api-production.august.com`
service and passive `https://ps.pndsn.com` subscriptions, not Home Assistant,
MQTT, Python, or 1Password at runtime. Only `AUG-SL05-M01-G01` locks where the
account has `superuser` access are supported. Select each by its exact 32-digit
hexadecimal ID, not its name; similarly named or other account records are not
selected automatically.

**Controls are blocked.** Lock Entities expose no `set` Operation, and the
runtime refuses to start if any binding sets `commands_enabled`. This guide does
not authorize any physical operation.

**Monitoring is proven only against synthetic tests.** They cover restart,
retained identity, enablement, history, missing Devices, and offline
replacements. Compatibility with the live August service and PubNub push is
unverified; see [Limits of the evidence](#limits-of-the-evidence).

## Configure and authorize

1. Copy `configs/august.example.yaml` to a private deployment location. Set a
   unique Adapter ID and NATS URL, then list 1–64 locks with unique stable
   `binding_key` values and exact `lock_id` values. Hearth does not list the
   account's locks, so copy each 32-digit ID from an existing tool that reads
   your August account. Names are optional presentation overrides; otherwise a
   bounded cloud name is used. Do not reuse the example IDs.
2. Generate a new UUID once, for example with `uuidgen`, and place it in
   `installation_id`. Preserve the same UUID across authorization and every
   restart; replacing it can require another verification email.
3. Create a private regular JSON file, readable by the companion user only
   (for example, mode `0600`), containing `{"email":"...","password":"..."}`.
   Set `credentials_file` to its path. The file is read once per invocation,
   limited to 16 KiB, and password whitespace is preserved. Never put the
   password or verification code in flags or environment variables.
4. Explicitly run `hearth-adapter-august authorize --config /path/to/august.yaml`.
   From a source checkout, run `go run ./cmd/hearth-adapter-august` in place of
   `hearth-adapter-august`; the release image is
   `ghcr.io/mholtzscher/hearth/hearth-adapter-august`.
   If the installation is not already validated, this sends at most one email
   and prompts for its six-digit code on stdin; it does not connect to Core or
   PubNub, save a token, or modify files. Ctrl-C interrupts terminal input. An
   already validated installation sends no email. Runtime never requests codes.
5. Start `hearth-adapter-august --config /path/to/august.yaml` alongside Core and
   NATS. Normal nonsecret flags and `HEARTH_ADAPTER_AUGUST_...` environment
   variables override YAML, as with other companions. There is no flag to enable
   all locks. Protect configuration because lock IDs and account context are private.

Polling defaults to 5 minutes for status and 15 minutes for inventory/detail.
Status can be 1 minute–1 hour; inventory/detail must be at least the status
interval and at most 1 hour. Polling, startup, authentication, recovery, and
PubNub traffic all use upstream resources; these settings are not a vendor quota
guarantee.

## Verify read-only monitoring

With an authorized account, selected lock IDs, and a running Core, NATS, and
companion, set `CORE` to the trusted Core HTTP base URL and `ADAPTER` to the
configured Adapter ID. These reads do not issue Commands:

```sh
CORE=http://127.0.0.1:8080
ADAPTER=august-home
curl -fsS "$CORE/v1/adapters/$ADAPTER"
curl -fsS "$CORE/v1/devices"
curl -fsS "$CORE/v1/entities?units=canonical"
```

Follow collection pagination and identify each selected lock's Device and its
Entities. Set `ENTITY` to a discovered Entity ID, then inspect its detail and
retained history:

```sh
curl -fsS "$CORE/v1/entities/$ENTITY"
curl -fsS "$CORE/v1/entities/$ENTITY/state/history"
```

Compare Adapter health, Entity availability, State, and observation times with
authorized read-only provider information. Missing or old evidence is not proof
of physical State. Investigate unavailable or absent Entities without sending
Commands or initiating physical transitions.

## Diagnose setup failures

Process failures report fixed `error_code` values. Configuration validation also
reports a path-free `reason`; malformed files do not echo rejected input.

| Code | Operator action |
| --- | --- |
| `credentials_file_unreadable` | Check that the configured file exists and the companion user can read it. |
| `credentials_file_invalid` | Use a private regular JSON file, at most 16 KiB, with email and password. |
| `auth_rejected` | Check the independently provisioned account credentials. |
| `validation_required` | Explicitly authorize the preserved installation UUID. Runtime sends no code. |
| `verification_input_invalid`, `verification_input_unreadable` | Supply one six-digit code on stdin. The process does not retry automatically. |
| `verification_rejected` | The validation endpoint rejected the attempt. Check the code and installation before another explicit authorization. |
| `throttled` | Wait for the account cooldown; do not run repeated authorization attempts. |
| `session_expired`, `upstream_rejected` | The service refused that authorization request. Check the installation and account before retrying. |
| `provider_unavailable`, `invalid_response` | Investigate service reachability or unofficial API compatibility. |
| `commands_blocked` | Set every `commands_enabled` to false. Lock control is not implemented. |

Unknown runtime failures keep the fixed `run_failed` classification rather than
printing dependency messages, credentials, file paths, or verification codes.

## What the readings mean

Each selected Device has `Lock`, `Door open`, and `Reported battery estimate`
Entities. Battery is a provider-reported percentage estimate, not a validated
remaining lifetime. DoorSense is independent of the bolt: a closed door does not
mean locked. `jammed` and `unknown` do not mean unlocked. Missing or invalid
fields do not become closed, unlocked, or zero; valid siblings remain separate.

Availability requires exact-ID detail reporting an operative, online Bridge and
valid field evidence. It is provider-reported reachability, not proof of recent
physical contact. Source timestamps may legitimately be old. Each field's
ordering high-water mark is kept independently in memory only; after a restart
there is no across-process source-order guarantee. Retained State/history and
operator overrides remain Core records, not new evidence from a replacement.
Push disconnects fall back to scheduled reads and reconnection/resynchronization;
there is no durable push replay or imported activity history.

## Limits of the evidence

Synthetic tests do not prove August compatibility. Read-only research reached
account, inventory, and detail access and a passive PubNub handshake, but
observed no push event. Live push payloads, transition delivery, response
fields, timestamp comparability, and native Go request framing are unverified.
Lock control needs its own live evidence, implementation, and a supervised
trial before any binding can enable it.
