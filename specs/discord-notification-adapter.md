# Discord notification adapter

Status: Contract-reviewed proposal; ready for implementation planning. Effort: L.
Depends on registration, runtime support and the wire contract in
[Core notifications](core-notifications.md).

## Purpose

Provide the first real notification integration as `hearth-discord`, a separate
process with provider credentials and no durable delivery queue. An incoming
webhook targets a configured ordinary Discord channel. Core remains vendor-neutral.

The adapter registers its configured destinations with Core on startup. Core
assigns stable IDs using adapter identity and local key. The operator selects a
registered destination in Automations; no manual destination or binding creation
is required.

## Configuration and mapping

```yaml
adapter_id: discord-main
destinations:
  household:
    name: Household alerts
    webhook_url_file: /run/secrets/discord-household-webhook
```

Follow repository flag/environment/YAML precedence for process settings. Routes
are YAML-only structured configuration. The full webhook URL is a credential;
read it from a secret file and exclude it from logs, errors and registration.
Configuration changes take effect on adapter restart for the initial version.

Proposed adapter-owned shapes:

```go
type DestinationConfig struct {
    Name string `yaml:"name"`
    WebhookURLFile string `yaml:"webhook_url_file"`
}
type Config struct {
    AdapterID string `yaml:"adapter_id"`
    Destinations map[string]DestinationConfig `yaml:"destinations"`
}
type Sender interface {
    Send(context.Context, string, adapter.NotificationMessage) adapter.NotificationOutcome
}
```

The existing adapter SDK supplies public message/outcome DTOs; adapter code must
not import Core module internals. Outcomes map to the canonical accepted,
rejected, not-sent and unknown alternatives with bounded reason codes.

## External behavior

- POST once to Execute Webhook with `wait=true`, using one embed with optional
  title and required description. Disable mention parsing.
- A validated successful message response establishes acceptance and supplies an
  opaque message receipt. Acceptance is not human receipt or acknowledgement.
- Unknown route fails before external submission. Explicit permanent refusals and
  rate limiting return rejection without retry. Ambiguous network failures and
  server responses that do not establish non-delivery return unknown.
- No automatic redirects or retries that might repeat a POST. Every request is
  bounded by Core's absolute deadline; an expired request does not call Discord.
- Do not truncate, split messages, create threads or create webhooks automatically.
- One external request per admitted invocation, with no retry or persistent
  deduplication framework. The SDK joins admitted handlers during shutdown.
- Respect Core's ten-second end-to-end deadline. Support parallel sends without
  a fixed concurrency limit, waiting queue or ordering guarantee.

[Discord's webhook documentation](https://discord.com/developers/docs/resources/webhook#execute-webhook)
states that `wait=true` waits for server confirmation and returns the created
message; the default false setting can fail to report an unsaved message.
Forum/media channels require thread handling and are outside this first slice.

## Layout

```text
cmd/hearth-discord/                new — executable entry point
internal/app/discord/              new — configuration, assembly and lifecycle
internal/adapters/discord/         new — webhook mapping and bounded HTTP client
deployments/                      modify — companion deployment and secret example
```

## Deliverables and acceptance

| ID | Outcome | Owner | Depends on | Acceptance |
|---|---|---|---|---|
| D1 | Webhook client and outcome mapping | adapters/discord | N2 wire contract | A1, A2 |
| D2 | Registering session-backed companion | app/discord, cmd, SDK | N0, N1, D1 | A3 |
| D3 | Deployment documentation and smoke evidence | deployments | D2, N3 | A4 |

- A1: A controlled HTTP server observes exactly one POST, `wait=true`, correct
  embed fields and disabled mentions. A message response returns its receipt.
- A2: Exercise 400/401/403/404, 429, server failure, malformed success, dropped
  response and deadline expiry; none causes a second POST or credential leakage.
- A3: Through real NATS, wrong-runtime, known-fenced and expired requests cannot
  begin a webhook call. Concurrent fencing cannot retract an already-admitted call.
  Startup registers configured keys and names without credentials; restart
  preserves IDs. Unknown local keys reject without HTTP submission; registration
  does not perform complete-inventory removal reconciliation.
  Shutdown bounds in-flight calls; provider failure does not crash Core.
- A4: With an operator-provided test webhook, manually send through the Core API;
  verify exactly one Discord message and matching Core receipt/history. This
  external check requires credentials and is not a default CI dependency.

Use focused mise tasks and `mise run validate` after integration.

## Resolved provider policy

- HTTP 200 with a valid JSON message containing a nonempty decimal-string ID is
  accepted. Store that ID as an opaque receipt; never fetch or reconcile it.
- HTTP 400: rejected `invalid_message`; 401/403: rejected `provider_forbidden`;
  404: rejected `destination_unknown`; 429: rejected `provider_rate_limited`.
- Other 4xx: rejected `provider_rejected`. 5xx, unexpected redirects/status,
  malformed success and post-submission transport failure: unknown
  `provider_error`, `invalid_response` or `transport_error` as appropriate.
- Unknown local key rejects before HTTP. Expiry before HTTP is not-sent
  `deadline_expired`. Once HTTP execution begins, network failure is conservatively
  unknown; do not build a byte-level delivery detector.
- Concurrent handlers have no fixed Core or SDK concurrency limit.
  A 429 rejects that invocation with no sleep, retry, rate-limit queue or cooldown
  scheduler. Subsequent invocations remain independent.
- Read at most 64 KiB of a success response before decoding its receipt. An
  oversized response is unknown `invalid_response`; never include raw response
  bodies or webhook URLs in public errors or logs.
- Report healthy after validated local configuration, serving subscription and
  registration are ready. This is adapter readiness, not proof Discord is reachable.
  Individual provider failures remain attempt outcomes rather than creating an
  adapter-wide health state machine or synthetic heartbeat probe.

## Startup and shutdown contract

Validate all local routes and load webhook secret files before claiming a session.
Require at least one destination; reuse Core key/name bounds and message limits.
Use HTTPS Discord incoming-webhook URLs for ordinary channels; reject URLs with
userinfo, fragments, thread configuration or a non-Discord host. Set `wait=true`
on the outbound request. Accept the production `discord.com` API webhook path;
tests inject an HTTP transport rather than widening production URL validation.
Never follow redirects. Keep the resolved credentials in adapter memory only.

Call SDK `StartNotificationServer` and wait for its returned handle, then register
each configured key/name, then report healthy. On shutdown close the server and
join in-flight handlers before releasing the Session. API throttling remains an
attempt result; there is no background Discord probe or health recovery scheduler.

For embed bounds, enforce both the Core code-point limits and Discord's effective
UTF-16 field limits (256 title, 4096 description); reject incompatible text as
`rejected / invalid_message` without truncation. A 400 response remains the fallback
for provider validation disagreement. Receipt JSON may include unknown Discord
fields; require only the bounded ID, not a complete Discord message model.

Setup documentation must describe the webhook channel requirement, secret file,
stable local-key identity, test-send workflow and non-retry behavior. The external
smoke check is explicit operator-driven validation, not a default build action.
