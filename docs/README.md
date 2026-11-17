# Hearth documentation

Start with the [project README](../README.md) for setup and the
[glossary](../GLOSSARY.md) for terminology. [Specs](../specs/) hold unfinished
work or proposals awaiting an owner decision.

## Architecture

- [Architecture](architecture/architecture.md): for implementers. Who owns Hearth's responsibilities and what constraints connect them?
- [Product direction](architecture/product.md): for product and UI contributors. Which household goals and boundaries govern choices?
- [Adapter health projection](architecture/adapter-health-projection.md): for Core maintainers and operators. How does canonical Adapter health become Entity availability, and why can it lag?
- [Durable outbox relays](architecture/outbox-relays.md): for Core maintainers. What survives publication faults, and why does lease expiry remain independent?

## Guides

- [August monitoring](guides/august.md): for operators. How do I configure and verify read-only lock monitoring?
- [Automation branching](guides/automation-branching.md): for Automation authors. How do I route a Run through nested If and Choose Steps?
- [Automation delays](guides/automation-delay-steps.md): for Automation authors. How do I add and inspect an elapsed wait?
- [Automation Function Steps](guides/automation-function-steps.md): for Automation authors. How do I invoke an Adapter Function from a Run?
- [Discord notifications](guides/discord.md): for operators. How do I configure and prove one notification send without replay?
- [Ecowitt station setup](guides/ecowitt.md): for operators. How do I connect a GW2000/WS90 station over MQTT and inspect its measurements?
- [ESPHome setup](guides/esphome.md): for operators. How do I configure Native API discovery, static devices and credentials safely?
- [ESPHome thermostats](guides/esphome-thermostat.md): for operators. How do I inspect thermostat support and safely read back a Command outcome?
- [Levoit purifiers](guides/levoit.md): for operators. How do I configure purifier control and interpret its optimistic evidence?
- [Application logging](guides/logging.md): for operators and developers. How do I capture diagnostics without confusing logs with durable outcomes or health?
- [Pirate Weather](guides/pirateweather.md): for operators. How do I configure current readings, forecast States and fresh forecast Functions?
- [Reusable Automations](guides/reusable-automations.md): for household operators. How do I define, call and diagnose a reusable sequence?
- [Storybook development](guides/storybook.md): for dashboard contributors. How do I run and verify component stories without Core?
- [Testing and validation](guides/testing.md): for contributors. How do I choose focused checks and complete validation?
- [Zigbee2MQTT setup](guides/zigbee2mqtt.md): for operators. How do I connect an existing Zigbee2MQTT service and verify discovery without device Commands?
- [Extend Zigbee2MQTT capabilities](guides/zigbee2mqtt-capabilities.md): for Adapter contributors. How do I add and prove a capability from captured evidence?
- [Z-Wave JS setup](guides/zwavejs.md): for operators. How do I connect an existing server while preserving network identity and physical-acceptance limits?

## Reference

- [Agent skills and provider continuation](reference/agent-skills.md): for agent maintainers. How do draft authorization, provider protocols and conversation continuation work?
- [Adapter Functions](reference/adapter-functions.md): for Adapter developers and API clients. What do discovery, invocation and retained evidence guarantee?
- [Automation Conditions](reference/automation-conditions.md): for Automation authors. When are Conditions evaluated, and what do three-valued results mean?
- [Definition diagnostics](reference/automation-definition-diagnostics.md): for API and MCP clients. How do I interpret diagnostic codes, paths and spans?
- [Definition storage and upgrades](reference/automation-definition-revisions.md): for storage maintainers. What retains a definition revision, and what do development cutovers preserve?
- [Automation expressions](reference/automation-expressions.md): for Automation authors. Which values and evaluation semantics are available?
- [Scheduled Triggers](reference/automation-schedules.md): for Automation authors. Which cron forms and clock-admission rules apply?
- [Step execution storage](reference/automation-step-executions.md): for API and storage implementers. What does the sparse execution trace prove?
- [Agent-first dashboards](reference/dashboards.md): for UI and client developers. How do view bindings, cards and controls behave?
- [History queries](reference/history-queries.md): for API clients. What do numeric buckets, coverage and query errors mean?
- [Ingress dispositions](reference/ingress-disposition-contracts.md): for transport maintainers. Why do Observation, Entity Event and availability faults have different recovery policies?
- [MCP results and failures](reference/mcp-errors.md): for MCP clients. How do modeled failures, validation and receipts differ from HTTP?
- [Notifications](reference/notifications.md): for operators and Automation authors. What does a retained notification Attempt establish?
- [Persistence error contracts](reference/persistence-error-contracts.md): for service and transport maintainers. Which error identities govern reconciliation and precedence?
- [Platform lifecycle and NATS](reference/platform.md): for contributors. Which shared owner should I reuse, and which policies remain module-owned?
- [Service input contracts](reference/service-input-contracts.md): for service callers. Which invalid-input distinctions are actionable?
- [Scripted simulator](reference/simulator.md): for contributors. How are scenarios declared and controlled, and what can their evidence establish?
- [SQLite mapping standards](reference/sqlite-mapping-standards.md): for persistence contributors. Which row-decoding, file-role and timestamp-compatibility rules apply?
- [State changes](reference/state-changes.md): for API clients. How are yes/no and text transitions represented and retained?
- [Quantity metadata and units](reference/units.md): for API and Adapter developers. How do canonical and household read representations differ?

## Decision records

Records describe decisions at their historical point, not a second current contract.

- [Replace Home Assistant functionally, not every protocol service](adr/0001-replace-home-assistant-functionally.md)
- [Earn every production use of NATS](adr/0002-earn-every-production-use-of-nats.md)
- [Isolate the temporary Home Assistant adapter behind NATS](adr/0003-isolate-home-assistant-behind-nats.md)
- [Omit sites from version one](adr/0004-omit-sites-from-version-one.md)
- [Require a fresh Observation for Command satisfaction](adr/0005-require-observation-for-command-success.md)
- [Assign canonical identities in Core](adr/0006-assign-canonical-identities-in-the-core.md)
- [Use expiring request/reply for immediate Commands](adr/0007-use-expiring-request-reply-for-commands.md)
- [Project retained Observations into SQLite](adr/0008-project-jetstream-observations-into-sqlite.md)
- [Use JSON Schema for version-one contracts](adr/0009-use-json-schema-for-version-one-contracts.md)
- [Order canonical State by Core receive order](adr/0010-order-state-by-core-receive-order.md)
- [Provide a thin Go Adapter SDK](adr/0011-provide-a-thin-go-adapter-sdk.md)
- [Record Command attempts without durable replay](adr/0012-record-command-attempts-without-replay.md)
- [Allow overlapping Commands per Entity](adr/0013-allow-overlapping-commands-per-entity.md)
- [Name the product Hearth and reserve hearthd for Core](adr/0014-name-product-hearth-and-core-daemon-hearthd.md)
- [Generate Entity-type behavior from manifests](adr/0015-generate-entity-type-behavior.md)
- [Fence each Adapter instance to one runtime](adr/0016-fence-adapter-instance-runtimes.md)
- [Model Adapter health and Entity availability](adr/0017-model-adapter-health-and-entity-availability.md)
- [Return a command-evidence capability from acceptance](adr/0018-return-command-evidence-capability.md)
- [Publish Device Facts over Core NATS](adr/0019-device-facts-over-core-nats.md)
- [Make Device Facts durable with an outbox](adr/0020-durable-device-facts-via-outbox.md)
- [Drive Automations from age-bounded Device Facts](adr/0021-drive-automations-from-device-facts.md)
- [Evaluate Conditions from coherent State snapshots](adr/0022-evaluate-conditions-from-state-snapshots.md)
- [Serve MCP from Core's HTTP server](adr/0023-serve-mcp-from-core-http-server.md)
- [Carry previous State on Observation Facts](adr/0024-carry-previous-state-on-observation-facts.md)
- [Schedule Automations without replay](adr/0025-schedule-automations-without-replay.md)
- [Scope persistence failures to operations](adr/0026-scope-persistence-failures-to-operations.md)
- [Draft: observed settings versus stateless dispatched actions](adr/draft-observed-settings-vs-stateless-dispatched-actions.md)
