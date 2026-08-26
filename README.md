# Hearth

Hearth is a home automation system for technical self-hosters. It is being designed to replace Home Assistant functionally for one household per deployment while retaining mature specialist protocol services where useful.

**Naming:** Hearth is the product and user-facing namespace. `hearthd` is reserved for the core daemon; companion processes use `hearth-` names.

The project has two equal gates: work must advance a useful home automation system and meaningful NATS learning, while every production use of NATS must solve a real system need.

## Status

Hearth's first vertical slice observes and controls one Home Assistant-managed light; the simulator exercises recovery and the complete failure matrix against the same contracts. The next approved direction turns that isolated process into a permanently supported inbound Home Assistant Bridge.

## Development

Enter the devenv shell with `devenv shell` and start local NATS/JetStream with `devenv up`. The clean-checkout gate is `devenv test`; it checks formatting, Entity-type and sqlc generation, all authoritative schemas and cross-binary fixtures, race-enabled tests (including runtime OpenAPI and recovery), and vetting. Regenerate complete built-in Entity-type bindings, behavior, conformance tests, typed SDK facades, and catalog assembly after changing a manifest, examples, or semantic schema with `go generate ./entitytypes`; regenerate database access code after changing migrations or queries with `devenv shell -- sqlc generate`.

Run the first-light simulator with `go run ./cmd/hearth-simulator -config configs/simulator.yaml` after copying `configs/simulator.example.yaml`. Its `scenario` may be `happy`, `duplicate`, `delayed-source-time`, `future-clock-skew`, `malformed`, `unavailable-adapter`, `upstream-rejection`, `no-op-refresh`, `overlapping-opposite-command`, `outcome-timeout`, `interrupted-command`, or `restart-before-ack`.

### Home Assistant adapter

The currently implemented first-light config still uses one explicit Binding. Copy `configs/homeassistant.example.yaml` to the ignored `configs/homeassistant.yaml`, configure one Home Assistant light, and place a long-lived access token at the configured ignored `token_file` path. With NATS and `hearthd` running, start the Adapter:

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

The implemented first-slice contract is [`specs/first-light.md`](./specs/first-light.md). The permanent Bridge foundation is specified in [`specs/home-assistant-bridge.md`](./specs/home-assistant-bridge.md).
