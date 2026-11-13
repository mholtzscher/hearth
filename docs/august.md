# August cloud monitoring companion

The August companion is a native Go, read-only monitor for explicitly selected US
August locks. It uses the unofficial fixed `https://api-production.august.com`
service and passive `https://ps.pndsn.com` subscriptions, not Home Assistant,
MQTT, Python, or 1Password at runtime. Only `AUG-SL05-M01-G01` locks where the
account has `superuser` access are supported. Select each by its exact 32-digit
hexadecimal ID, not its name; similarly named or other account records are not
selected automatically.

**Controls are blocked.** `commands_enabled` must remain false; the current
runtime refuses to start if any binding enables it. The direct live evidence
gate (D4/A9), command implementation (D5), and supervised trial (A13) have not
been completed. This guide does not authorize any physical operation.

**Monitoring is not yet deployment-validated.** The synthetic production-Core
journey covers restart, retained identity, enablement, history, missing Devices,
and offline replacements. Native Go framing and actual lock/push behavior still
need the deferred live checks below before rollout.

## Configure and authorize

1. Copy `configs/august.example.yaml` to a private deployment location. Set a
   unique Adapter ID and NATS URL, then list 1–64 locks with unique stable
   `binding_key` values and exact IDs. Names are optional presentation overrides;
   otherwise a bounded cloud name is used. Do not reuse the example IDs.
2. Generate a new UUID once, for example with `uuidgen`, and place it in
   `installation_id`. Preserve the same UUID across authorization and every
   restart; replacing it can require another verification email.
3. Create a private regular JSON file, readable by the companion user only
   (for example, mode `0600`), containing `{"email":"...","password":"..."}`.
   Set `credentials_file` to its path. The file is read once per invocation,
   limited to 16 KiB, and password whitespace is preserved. Never put the
   password or verification code in flags or environment variables.
4. Explicitly run `hearth-adapter-august authorize --config /path/to/august.yaml`.
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
interval and at most 1 hour. Two locks at the defaults produce 576 scheduled
status reads and 288 inventory/detail reads daily, plus startup, authentication,
recovery, and PubNub traffic. This is arithmetic, not a vendor quota guarantee.

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
| `commands_blocked` | Leave every `commands_enabled` setting false until the live evidence and implementation gates pass. |

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

## Evidence and live gates

Synthetic tests do not prove August compatibility. Earlier read-only research
established account and inventory/detail access and a passive PubNub handshake,
but no push event was observed. Live push payloads, transition delivery, response
fields and timestamp comparability remain unverified. Go's HTTP transport omits
`Content-Length: 0` on GET despite the request header; an external read-only
probe stopped at 1Password before making HTTP requests. Actual native-Go framing
compatibility has **not** been verified.

Before any future control work, obtain separate permission for manual transitions
on each lock and record sanitized push/time evidence. With further explicit
permission, inspect direct synchronous lock and unlock responses on both firmware
versions, including noncached acceptance, matching lock identity, terminal State,
and operation-start versus report timestamp comparability. Check unchanged
DoorSense independently. If the contract is not established, stop and revise it;
do not weaken the checks. These tests have not been run.

Only after that gate and implementation/tests pass could a separately authorized,
supervised, one-binding-at-a-time Core trial be planned with safe access and an
agreed restoration procedure. The normal configuration stays read-only. Stop
and join the trial process, resume monitoring, and verify `set` is absent; only
recorded passing trial results could support a later operator rollout decision.
Do not infer permission to unlock from a restoration request. No trial has run.

The planned command contract does not use optimistic confirmation, mutation
retries, queued replay, or token reuse across processes. Acceptance alone would
not mean satisfaction: only retained, linked matching direct-result evidence
could satisfy a Command. Lost/indeterminate responses can leave physical effect
uncertain; never automatically retry. Current monitoring exposes no `set`.
