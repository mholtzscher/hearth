# Product Direction

**Status:** Evolving

## Purpose

Hearth is a dependable home automation system that also provides meaningful, production-grounded experience with NATS. Neither goal alone is sufficient: milestones must advance both.

## Audience

Hearth initially serves technical self-hosters. Each trusted deployment owns one household; version 1 does not model a separate Site or Home object. Making installation reproducible for other households matters, but multi-tenant hosting and one-deployment/many-home operation do not.

## Product boundary

Hearth aims to become a [functional replacement](../CONTEXT.md) for Home Assistant without requiring every Household to eliminate it. The Home Assistant Bridge is a permanently supported inbound Adapter for objects whose configuration and lifecycle remain owned by Home Assistant. A bridged object may remain there indefinitely or later move to native ownership without changing its canonical Hearth IDs. Hearth may also retain mature specialist protocol services rather than reimplementing their device protocols.

Every production use of NATS must solve a concrete need involving durability, isolation, routing, or observability. Exercising a NATS capability is not by itself a reason to put that capability into the product.

## First vertical slice

Observe and control one light still managed by Home Assistant through an HTTP API. This first slice validated the isolated Adapter seam while keeping the house operational. The Home Assistant Bridge runs separately from the core, and a simulator injects failures against the same contracts. The next foundation makes that Bridge permanently supportable; see [`specs/home-assistant-bridge.md`](../specs/home-assistant-bridge.md). Configurable automations and a browser interface remain deferred.

## Explicit non-goals

- Reimplement mature radio or device protocol stacks merely to remove a dependency.
- Build a multi-tenant hosted control plane.
- Operate several unrelated households from one deployment.
- Force every NATS capability into production architecture.
- Pursue broad Home Assistant ecosystem parity before satisfying concrete household needs.
