# Discord companion

See [Discord setup and operator smoke validation](../docs/discord.md).
`discord.compose.yaml` adds only the companion; supply your existing Core/NATS.
Copy `../configs/discord.example.yaml` to `discord.yaml` here and replace
`nats_url` with the reachable trusted-private NATS address (container localhost
does not point at the host). Place the full webhook URL in
`secrets/discord-household-webhook` (see the non-secret
`discord-household-webhook.example` shape), with restrictive permissions and readable by
the container user. Neither file belongs in Git. Then run:

```sh
docker compose -f deployments/discord.compose.yaml up -d
```

Use a pinned release image in production. Outbound HTTPS to `discord.com` is
required. The companion needs no inbound port, persistent volume, or delivery
queue. Its twenty-second termination grace covers cancellation/join and runtime
release. Restarting never replays sends; Core retains attempt history.
