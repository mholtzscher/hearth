# Hearth

Hearth is a home automation system for technical self-hosters. It is being designed to replace Home Assistant functionally for one household per deployment while retaining mature specialist protocol services where useful.

**Naming:** Hearth is the product and user-facing namespace. `hearthd` is reserved for the core daemon; companion processes use `hearth-` names.

The project has two equal gates: work must advance a useful home automation system and meaningful NATS learning, while every production use of NATS must solve a real system need.

## Status

Hearth's first vertical slice observes and controls one Home Assistant-managed light; the simulator exercises recovery and the complete failure matrix against the same contracts.

## Development

Install tools with `mise install` and start local NATS/JetStream with `mise run nats`. The validation gate is `mise run validate`; it regenerates checked-in code, formats Go files, tidies module metadata, and then checks generation, formatting, module tidiness, linting, all authoritative schemas and cross-binary fixtures, race-enabled tests (including runtime OpenAPI and recovery), vetting, and no-push multi-platform release container builds. Regenerate checked-in Entity-type and database access code after changing its inputs with `mise run generate`.

`hearthd` accepts any configured HTTP bind address. The example remains `127.0.0.1:8080`; bind to a non-loopback address only on a trusted network because the HTTP API has no authentication.

Run the first-light simulator with `go run ./cmd/hearth-simulator -config configs/simulator.yaml` after copying `configs/simulator.example.yaml`. Its `scenario` may be `happy`, `duplicate`, `delayed-source-time`, `future-clock-skew`, `malformed`, `unavailable-adapter`, `upstream-rejection`, `no-op-refresh`, `overlapping-opposite-command`, `outcome-timeout`, `interrupted-command`, or `restart-before-ack`.

### Home Assistant migration adapter

Copy `configs/homeassistant.example.yaml` to the ignored `configs/homeassistant.yaml`, configure one Home Assistant light, and place a long-lived access token at the configured ignored `token_file` path. With NATS and `hearthd` running, start the disposable adapter:

```sh
go run ./cmd/hearth-adapter-homeassistant -config configs/homeassistant.yaml
```

The registration log reports the canonical Entity ID. Use it to verify the snapshot and a real on/off command; a successful command response is returned only after the adapter publishes its linked refresh Observation:

```sh
curl http://127.0.0.1:8080/v1/entities/ent_...
curl -X POST http://127.0.0.1:8080/v1/entities/ent_.../commands \
  -H 'content-type: application/json' \
  -d '{"operation":"set","parameters":{"value":true}}'
```

## Documentation

- [`CONTEXT.md`](./CONTEXT.md): canonical project language
- [`docs/product.md`](./docs/product.md): audience, goals, boundaries, and success
- [`docs/architecture.md`](./docs/architecture.md): current accepted architectural constraints
- [`docs/adr/`](./docs/adr/): durable architectural decisions and their rationale
- [`docs/plans/`](./docs/plans/): implementation plans
- [`specs/`](./specs/): approved implementation-ready specifications

The approved first-slice contract is [`specs/first-light.md`](./specs/first-light.md).
