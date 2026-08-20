# Product Direction

**Status:** Evolving

## Purpose

Hearthd is a dependable home automation system that also provides meaningful, production-grounded experience with NATS. Neither goal alone is sufficient: milestones must advance both.

## Audience

Hearthd initially serves technical self-hosters. Each trusted deployment owns one household; version 1 does not model a separate Site or Home object. Making installation reproducible for other households matters, but multi-tenant hosting and one-deployment/many-home operation do not.

## Product boundary

Hearthd aims to become a [functional replacement](../CONTEXT.md) for Home Assistant. The Home Assistant adapter is disposable migration infrastructure that keeps this household running while native adapters take ownership device by device. It is deleted after migration and is not a permanently supported bridge or part of the intended final deployment. Hearthd may retain mature specialist protocol services rather than reimplementing their device protocols.

Every production use of NATS must solve a concrete need involving durability, isolation, routing, or observability. Exercising a NATS capability is not by itself a reason to put that capability into the product.

## First vertical slice

Observe and control one light still managed by Home Assistant through an HTTP API. This validates the temporary migration seam while keeping the house operational. The Home Assistant adapter runs separately from the core, and a simulator injects failures against the same contracts. Configurable automations and a browser interface are deferred. See [`plans/0001-first-light.md`](./plans/0001-first-light.md).

## Explicit non-goals

- Reimplement mature radio or device protocol stacks merely to remove a dependency.
- Build a multi-tenant hosted control plane.
- Operate several unrelated households from one deployment.
- Force every NATS capability into production architecture.
- Pursue broad Home Assistant ecosystem parity before satisfying concrete household needs.
