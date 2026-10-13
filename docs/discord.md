# Discord notifications

`hearth-adapter-discord` is a stateless companion using the public Adapter SDK. Create
an **incoming webhook in an ordinary Discord text channel** you control. Forum
and media channels requiring threads are unsupported; the adapter never creates
webhooks, channels, or threads. Treat the entire URL as a credential.

Copy `configs/discord.example.yaml` to ignored `configs/discord.yaml`. Put the
full `https://discord.com/api/webhooks/<decimal-id>/<token>` URL in a separate
regular secret file (optional trailing newline), readable only by the operator
and process. Set `webhook_url_file` to that file, never put the URL in YAML,
flags, environment, logs, or registration. Versioned `/api/vN/webhooks/...` paths
are also accepted; userinfo, alternate hosts, fragments and query/thread settings
are rejected. The adapter adds `wait=true` itself.

```sh
go run ./cmd/hearth-adapter-discord --config configs/discord.yaml
```

Process settings resolve flags > `HEARTH_ADAPTER_DISCORD_` environment > YAML > defaults.
Flags are `--config`, `--adapter-id`, `--nats-url`, `--log-level`, `--log-format`;
environment suffixes are `CONFIG`, `ADAPTER_ID`, `NATS_URL`, `LOG_LEVEL`,
`LOG_FORMAT`. The implicit `configs/discord.yaml` is optional; an explicit file
must exist. NATS defaults to `nats://127.0.0.1:4222`; logging defaults to info/text
and is not YAML configuration. **Destinations are YAML-only**. At least one
destination is required. Changes and secret rotation require restart.

Keep `adapter_id` and each local key stable: together they determine Core's
stable destination identity. Display-name or webhook changes preserve that ID.
Omitted routes are not deleted from Core's inventory; they become unavailable
until registered by the active runtime. Local configuration and all secrets are
validated before claiming a Session; serving starts before registration and
healthy reporting. Healthy means ready to serve, not a Discord connectivity
probe. Individual provider failures do not alter process health or crash Core.

Each invocation makes at most one POST, one embed, with mention parsing disabled.
Titles are optional (256 code points and 256 UTF-16 units); descriptions are
required (2000 code points, at most 4096 UTF-16 units). Incompatible text is
rejected, never truncated or split. HTTP 200 with a bounded decimal message ID
means accepted; its receipt proves provider acceptance, not human receipt.
Permanent refusals and 429 reject without sleeping/retrying. Server failures,
malformed successes and post-submission transport errors are unknown. Redirects
are never followed. All work respects the absolute Core deadline; shutdown
cancels and joins admitted handlers before releasing the Session. Concurrent
sends have no waiting queue, ordering guarantee or fixed concurrency cap.

## Operator-only A4 smoke check

**Not executed as part of automated validation: requires an operator-provided
test webhook.** Run Core and this companion against the same trusted NATS, then:

1. Discover `GET /v1/notification-destinations` and locate Household alerts.
2. Submit exactly one uniquely worded test message:

   ```sh
   curl -X POST http://127.0.0.1:8080/v1/notification-destinations/<ndst_id>/notifications \
     -H 'Content-Type: application/json' \
     -d '{"title":"Hearth smoke","message":"Operator test: replace with unique timestamp"}'
   ```

3. Record the returned `ntf_` ID, `accepted` status and decimal receipt.
   Inspect `GET /v1/notifications/<ntf_id>` and destination-filtered history.
4. Verify **exactly one** matching message in Discord, with that message ID
   (enable Developer Mode to copy its ID). Record time, release, destination ID,
   attempt ID and matching receipt; never record the webhook URL.
5. Restart the companion and verify destination ID stability without another send.

An ambiguous outcome is not permission to retry. Inspect Core history and the
channel first; every new invocation creates a new attempt and may duplicate a
message. See [Core notifications](notifications.md) for outcome/history details
and [deployment guidance](../deployments/README.md) for the companion container.
