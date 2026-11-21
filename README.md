# Hearth

Hearth is a home automation system for technical self-hosters. It keeps
mature specialist services, such as Zigbee2MQTT and Z-Wave JS, where useful.

## What it does

- Native companion Adapters connect devices and services to Core.
- Typed Entities separate reported State, availability, and Command outcomes.
  Acceptance is not proof of a physical effect.
- Automations run bounded Trigger, Condition, and Step definitions, retaining
  execution evidence without automatic effect replay.
- The browser offers conversation, live views, saved dashboards, and inspection.
  Agent-generated drafts require an explicit browser Save.

Core uses NATS/JetStream for messaging and SQLite for canonical records and
history. `hearthd` names the Core daemon; companions use `hearth-` names.
See [product direction](docs/architecture/product.md) and
[architecture](docs/architecture/architecture.md) for boundaries and ownership.

## Before you start

- **Use loopback or a trusted private network.** Hearth's HTTP API has no
  authentication. Broker access also exposes household data and control;
  individual Adapter guides describe their upstream security limits.
- **Core requires a model credential.** Model requests can incur provider
  charges. Keep credentials out of YAML, command arguments, shell history,
  logs, and committed files.
- **Simulation is not hardware acceptance.** Check each Adapter's compatibility
  and evidence limitations before connecting physical devices. Physical
  Commands need deliberate authorization, not an automated smoke check.
- **Back up before upgrades.** Development cutovers can reset data or prevent
  lossless downgrade. Use a consistent SQLite backup, including its WAL where
  applicable, and restore with its matching binary. See
  [timestamp compatibility](docs/reference/sqlite-mapping-standards.md#make-timestamp-compatibility-explicit)
  and [Automation upgrades](docs/reference/automation-definition-revisions.md#upgrade-and-rollback).

## Try it with simulated devices

Install [Mise](https://mise.jdx.dev/) and have `curl` and `jq` available.
Run from the repository root:

```sh
mise install
mise run web-install
```

The [simulator Core configuration](configs/hearthd.simulator.yaml) selects OpenCode Go. Supply a
nonempty `HEARTH_OPENCODE_GO_API_KEY` through your supervisor or secret manager
before starting. Household-assistant eligibility for Go remains unconfirmed;
review the [provider notes](docs/reference/agent-skills.md#opencode-go).
If you explicitly select OpenAI instead, supply `HEARTH_OPENAI_API_KEY`.
Neither provider's key substitutes for the other's.

```sh
mise run simulator-start
mise env --json | jq '{SIM_CORE_PORT, SIM_WEB_PORT}'
```

Open `http://127.0.0.1:<SIM_WEB_PORT>/` using the printed dashboard port.
The managed stack runs worktree-local NATS/JetStream, Core, scripted Devices,
and the browser. It does not start MQTT or optional physical-device Adapters.
Do not print the complete environment; it can contain credentials.

Check the selected Core's readiness:

```sh
core_port=$(mise env --json | jq -er .SIM_CORE_PORT)
curl -fsS "http://127.0.0.1:$core_port/readyz"
```

Readiness describes Core dependencies, not device health or physical outcomes.
Use the browser's Adapter, Entity, and Command reads for their separate evidence.
The [simulator reference](docs/reference/simulator.md) describes scenarios,
fault controls, notifications, and Functions.

When finished:

```sh
mise run simulator-stop
```

This stops only the worktree's simulator stack and preserves its data.

## Run Core with native Adapters

For a non-simulator deployment, provision NATS/JetStream and any upstream
specialist services separately. Loopback addresses in examples are defaults,
not provisioned brokers.

```sh
cp -n configs/hearthd.example.yaml configs/hearthd.yaml
mkdir -p .data
```

Edit the private config for your household timezone, trusted HTTP bind address,
NATS endpoint, SQLite path, and model provider. Supply the selected provider's
credential before launch: `HEARTH_OPENAI_API_KEY` for the default OpenAI provider,
or `HEARTH_OPENCODE_GO_API_KEY` for OpenCode Go.

```sh
go run ./cmd/hearthd --config configs/hearthd.yaml
```

Core settings resolve from CLI flags, `HEARTHD_` environment variables, YAML,
then defaults. An explicitly selected config must exist. Use
`go run ./cmd/hearthd --help` and the
[example config](configs/hearthd.example.yaml) for current settings; see
[provider continuation](docs/reference/agent-skills.md) before switching providers.

Choose the setup guide for your companion:

| Adapter | Setup and limitations |
| --- | --- |
| Zigbee2MQTT | [Lights, plugs, sensors, and event sources](docs/guides/zigbee2mqtt.md) |
| Z-Wave JS | [Switches and dimmers through an existing server](docs/guides/zwavejs.md) |
| ESPHome | [Native API discovery and static devices](docs/guides/esphome.md) |
| Ecowitt | [GW2000/WS90 weather measurements over MQTT](docs/guides/ecowitt.md) |
| Pirate Weather | [Current conditions and forecasts](docs/guides/pirateweather.md) |
| Levoit | [VeSync air purifiers and optimistic Command evidence](docs/guides/levoit.md) |
| HomeKit | [Selected Devices in Apple Home and Siri](docs/guides/homekit.md) |
| Discord | [Notification destinations and one-shot sends](docs/guides/discord.md) |
| Music Assistant | [Discover players and library media, or play a track](docs/reference/adapter-functions.md#music-assistant-discovery-and-playback) |
| August | [Read-only lock monitoring; controls remain blocked](docs/guides/august.md) |

## Development

Choose focused checks using the [testing guide](docs/guides/testing.md), then
run the completion gate with `GO_PACKAGES` unset:

```sh
GO_PACKAGES='./contracts/functionschema' mise run test
mise run validate
```

Local Go tests are non-race by default; CI enables race detection. Full validation
also generates and formats code, tidies modules, checks frontend code, and runs
browser stories/builds. Real-Mosquitto tests need Docker; browser checks need
the platform-specific setup in the [Storybook guide](docs/guides/storybook.md).

- [Coding standards](CODING_STANDARDS.md) govern changes.
- [Entity-type authoring](entitytypes/README.md) covers schemas and generated behavior.
- [Application logging](docs/guides/logging.md) covers safe diagnostics and evidence.
- [Verification recipes](.agents/skills/verify-hearth/features/README.md) cover
  bounded simulator journeys, including Core-only recovery.

## Documentation

Use the [documentation index](docs/README.md) to find guides, references, and
decision records by reader and question. The [glossary](GLOSSARY.md) defines
canonical language. [Specs](specs/) hold unfinished work and a superseded
proposal awaiting an owner decision.
