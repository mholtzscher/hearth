# Hearthd

Hearthd is a home automation system for technical self-hosters. It is being designed to replace Home Assistant functionally for one household per deployment while retaining mature specialist protocol services where useful.

The project has two equal gates: work must advance a useful home automation system and meaningful NATS learning, while every production use of NATS must solve a real system need.

## Status

Hearthd is in design. The approved first vertical slice will observe and control one Home Assistant-managed light; a simulator will exercise failures against the same contracts.

## Documentation

- [`CONTEXT.md`](./CONTEXT.md): canonical project language
- [`docs/product.md`](./docs/product.md): audience, goals, boundaries, and success
- [`docs/architecture.md`](./docs/architecture.md): current accepted architectural constraints
- [`docs/adr/`](./docs/adr/): durable architectural decisions and their rationale
- [`docs/plans/`](./docs/plans/): implementation plans
- [`specs/`](./specs/): approved implementation-ready specifications
- [`docs/drafts/`](./docs/drafts/): non-authoritative source material and proposals

The approved first-slice contract is [`specs/first-light.md`](./specs/first-light.md).
