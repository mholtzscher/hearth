# Companions

## HomeKit bridge

See [Apple Home setup and acceptance](../docs/homekit.md). The outgoing
`hearth-bridge-homekit` uses a separate Node.js image,
`ghcr.io/mholtzscher/hearth/hearth-bridge-homekit`, with repository release tags.
Use Linux host networking, a persistent private data mount, readonly config and
setup-code mounts, and an unprivileged user. Do not use ordinary port publishing
as a substitute for LAN multicast, or assume advertisement interface selection
restricts TCP binding. Operators manage trusted-LAN firewall rules. Retain the
entire stopped data directory for pairing backups; ordinary startup never
initializes missing identity. Allow at least 15 seconds termination grace.

## Discord companion

See [Discord setup and operator smoke validation](../docs/discord.md).
Deploy `hearth-adapter-discord` with your existing Core/NATS using your own
process supervisor or container configuration; no Compose file is provided.
The release image is `ghcr.io/mholtzscher/hearth/hearth-adapter-discord`.
Use a pinned release tag in production.

Copy `configs/discord.example.yaml` to an operator-managed `discord.yaml` and
set `nats_url` to the reachable trusted-private NATS address (container localhost
does not point at the host). For a container, mount the configuration read-only
at `/etc/hearth/discord.yaml` and pass `--config /etc/hearth/discord.yaml`.
Place the full webhook URL in a separate regular secret file (see the non-secret
`discord-household-webhook.example` shape), with restrictive permissions and
readable by the process user. Mount it read-only at the configured
`webhook_url_file`, such as `/run/secrets/discord-household-webhook`.
Neither the operator configuration nor the secret belongs in Git.

Outbound HTTPS to `discord.com` is required. The companion needs no inbound
port, writable persistent volume, or delivery queue. It can run with a read-only
filesystem and no-new-privileges enabled. Allow a twenty-second termination
grace for cancellation/join and runtime release. Restarting never replays sends;
Core retains attempt history.
