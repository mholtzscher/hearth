# Distinguish observed settings from stateless dispatched actions

**Status:** DRAFT (unnumbered; see `specs/z2m-bulb-attributes.md` D9). Not accepted. Number only on acceptance.

Observable settings (`hearth.enumsetting/v1`, `hearth.numericsetting/v1`) hold a persistent choice that a command changes and a fresh linked observation confirms, completing as `satisfied`. Stateless actions (`hearth.enumaction/v1`, first used by the Zigbee2MQTT `effect` expose) request something with no reported state — an effect cannot be read back — so the command completes as honest unverified `dispatched` once adapter acceptance is durably recorded, with no observation requested, no matcher installed, and no state stored.

We chose two small generic types with type-level catalog policy (required per-operation `outcome`, manifest `stateless` flag) over one merged enum-control type, because a merged type would need per-entity statefulness and outcome policies instead, and would pretend an effect has state. Fabricating state or reusing `satisfied` for a dispatch was rejected as dishonest: `dispatched` carries no observation ID or value, stateless types reject every observation with `invalid_value`, and their reads stay `state: null`.

**Consequences:** HTTP and UI render a `dispatched` vocabulary distinct from `satisfied`; MQTT QoS 1 may duplicate deliveries and there is no automatic retry. **Evidence:** the covering inventory fixture is synthetic and Wanda-shaped; startup on-wire values and effect dispatch outcomes are unobserved until separately authorized live validation (G2). Dispatch alone makes no claim about any physical effect.
