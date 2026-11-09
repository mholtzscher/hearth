# Product Direction

<!-- impeccable:product-schema 1 -->

**Status:** Evolving

## Platform

web

## Purpose

Hearth is a dependable home automation system. Useful household behavior is the primary goal; meaningful, production-grounded experience with NATS is secondary. Milestones must serve real household needs rather than justify a technology exercise.

## Audience

Hearth initially serves technical self-hosters. Each trusted deployment owns one household; version 1 does not model a separate Site or Home object. Making installation reproducible for other households matters, but multi-tenant hosting and one-deployment/many-home operation do not.

## Product boundary

Hearth aims to become a [functional replacement](../GLOSSARY.md) for Home Assistant. The Home Assistant adapter is disposable migration infrastructure that keeps this household running while native adapters take ownership device by device. It is deleted after migration and is not a permanently supported bridge or part of the intended final deployment. Hearth may retain mature specialist protocol services rather than reimplementing their device protocols.

Every production use of NATS must solve a concrete need involving durability, isolation, routing, or observability. Exercising a NATS capability is not by itself a reason to put that capability into the product.

## Positioning

Hearth combines native Adapter ownership, typed Entity State and Commands, and retained execution evidence with an agent-first browser workflow. Conversation produces live views that the household can edit and explicitly save as independent dashboards. Home Assistant replacement means satisfying the household's required behavior, not matching its entire ecosystem.

## Operating Context

A technical self-hoster installs and operates Hearth and its supporting services on a trusted network. Migration proceeds Device by Device while the household remains operational.

The primary browser workflow is conversation, generated live view, customization, and explicit dashboard save. Saved dashboards reopen independently of their originating conversations and display current data without another model request. Diagnostic tools are secondary navigation, not the primary household experience.

## Capabilities and Constraints

- Home provides the conversation workspace and live canvas. Generated views and manual edits remain drafts until explicitly saved.
- Saved dashboards retain resource references, not captured readings. Rendering, refreshing, or saving a dashboard must not execute device Commands; only a household member's gesture on a control sends one.
- View cards show readings, trends, ranges, and charts. ToggleTile offers whole-card power control, with an optional explicitly selected metering reading and sparkline; ToggleList offers per-row power switches and optional Turn all off. Device cards for lights retain their power switch and brightness slider. Controls derive from Entity support. Controls for other Entity types remain a current limitation, not a product non-goal.
- Core exposes HTTP and MCP interfaces for inspection and supported actions. The household agent uses the same MCP catalog rather than a separate control mechanism.
- Automations support Triggers, Conditions, and ordered Steps, with retained execution history. Commands distinguish admission, dispatch, and observed satisfaction; acceptance alone does not establish a physical outcome.
- The current HTTP API has no authentication. Deployment remains limited to loopback or a trusted private network; public exposure is unsupported.
- Canonical terminology comes from the [glossary](../GLOSSARY.md). In particular, State, Entity availability, Adapter health, and Command outcome are distinct facts.

Current browser behavior and dashboard limitations are documented in [Agent-first dashboards](dashboards.md). Technical contracts and implementation details remain in their owning specifications and guides.

## First vertical slice

The initial milestone was to observe and control one Home Assistant-managed light through an HTTP API, validating the temporary migration seam while keeping the house operational. Its design separated the Home Assistant adapter from Core and used a simulator to inject failures against the same contracts. See [the first-light plan](plans/0001-first-light.md).

The project now includes configurable automations and a browser interface. The first-light milestone is historical context, not the current capability boundary.

## Evidence on Hand

- [Agent-first dashboards](dashboards.md) documents the implemented conversation, draft, save, live-data, and compatibility contracts.
- [The web application](../web/src/App.tsx) implements Home, Dashboards, and secondary Development navigation. Its existing stack is React, TypeScript, and Vite.
- [The README](../README.md) documents adapter coverage, setup, agent behavior, and feature-specific validation limits. Its introductory equal-goals statement predates the household-first priority recorded here.
- [The simulator configuration](../configs/scripted.simulator.yaml) supplies simulated Devices and fault scenarios. Simulation is not evidence of physical-device behavior.
- [Design artifacts](design/) contain prototypes, captures, and recordings. They are historical references, not binding visual requirements or proof of the current rendered interface.

## Product Principles

- Prioritize useful, dependable household behavior over NATS learning or broad ecosystem parity.
- Keep the household operational during migration, and remove the Home Assistant dependency when migration is complete.
- Make conversation the primary browser workflow while allowing explicitly saved dashboards to work independently of conversation and model requests.
- Preserve explicit intent and truthful evidence: a draft is not a save, a reading is not a requested value, and an accepted action is not proof of its physical outcome.

## Explicit non-goals

- Reimplement mature radio or device protocol stacks merely to remove a dependency.
- Build a multi-tenant hosted control plane.
- Operate several unrelated households from one deployment.
- Force every NATS capability into production architecture.
- Pursue broad Home Assistant ecosystem parity before satisfying concrete household needs.
