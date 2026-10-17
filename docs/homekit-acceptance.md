# HomeKit production-build acceptance

## Tested artifact and limits

Tests and operator checks used this implementation based on `a6335a3`, before
publication. This records observed behavior, not a release or Apple certification.
No Core API, wire schema, ownership, or Go production code changed.

## Automated results

| Boundary | Result |
|---|---|
| Strict TypeScript build and frozen patched installation | Passed |
| Package checks | 106 passed |
| Real Core/NATS/simulator integration | 23 passed |
| Linux amd64 nonroot host-network image | Built; encrypted pairing, real Commands, authenticated restart, and stable AID/IIDs passed |
| Dashboard tests/build | 69 tests and build passed |
| Go lint/vet, authoritative generation, cross-binary fixtures | Passed |
| Full local validation | Passed after the operator repaired Docker/Ryuk log retrieval |

The dependency negative controls establish that ordinary async setters emit a
stale requested value after newer State, and a pairing write failure without the
registered persistence handler leaves an encrypted server serving unsaved
in-memory pairing data. The corresponding production tests verify authoritative
callback completion and immediate failure-stop behavior. Literal false and
brightness zero are checked as actual encrypted HTTP responses, not controller
request echoes. HAP serializes power booleans as numeric zero/one on the wire.

Process checks include pairing/identifier retention, removal/restoration of
optional brightness, corrupted counters and duplicate IDs, missing identity,
pair/add/remove persistence failures, Core/NATS recovery, metadata-only changes,
native null State, opposite/repeated writes, total deadlines, readiness barriers,
two failed listener probes, admission rejection during draining, supervisor death,
and a hung dependency cleanup. These are executable checks, not an exhaustive
proof of every concurrency schedule or filesystem/power-loss failure.

## iPhone results

On 2026-10-17 UTC, the operator paired the production CLI with an on/off light,
dimmable light, and outlet backed by the managed scripted simulator. The iOS
version was not collected.

- Power toggles, brightness including zero, and rapid slider changes worked.
  Core recorded 6 lamp power, 6 dimmer power, 28 brightness, and 5 outlet Commands;
  all 45 were satisfied with linked outcome Observations.
- Simulated physical lamp/outlet changes and brightness at 20 percent updated
  Apple Home without interaction.
- Restarting with preserved storage retained room assignment and pairing;
  authenticated control worked without adding accessories again.
- Stopping Core produced Apple Home's communication-error presentation. After
  restoring Core, power and dimming control resumed without re-pairing.
- After recovery, physical off updates and brightness at 85 percent converged.
  The dimmer remained off while its separate power State was false. Publishing
  power-on independently then displayed on at 85 percent, as the operator
  confirmed. Brightness did not invent an extra power Command.

Representative durable Command evidence:

| Profile | Command | Outcome Observation |
|---|---|---|
| On/off lamp | `cmd_01a147a8-b355-7a48-bc69-aca7787e3ece` | `obs_01a147a8-b359-722f-920a-6114fe03d012` |
| Dimmable light | `cmd_01a147a8-bf33-78c5-8593-8c6b65ef75d6` | `obs_01a147a8-bf40-720e-bc8a-8d7d33836891` |
| Brightness | `cmd_01a147a8-dc75-7a7f-a61e-e044001ba08f` | `obs_01a147a8-dc79-7d0a-bf8b-010cbdf2b1d4` |
| Outlet | `cmd_01a147a8-b674-7245-9a01-aa9043e39d4d` | `obs_01a147a8-b67e-709f-9c98-23ad9165b7bd` |

Physical-change evidence after Core recovery includes lamp
`obs_01a147ad-e9b3-779f-a248-cce7580c9315`, outlet
`obs_01a147ad-e9d1-7cd2-b47c-a8011af017f2`, brightness
`obs_01a147ad-e9fd-7697-8640-391ab263d038`, and independent dimmer power
`obs_01a147ae-f5c1-760f-baa5-45f810722ec7`.

No physical hardware, remote Apple access, or iPhone-driven NATS outage was
evaluated. NATS recovery has separate automated process evidence. No pairing
keys, setup codes, local addresses, raw State traces, or screenshots are included
in this report.

## Repository validation history

The MQTT failures reproduce on unchanged Go baseline `a6335a3`. Ryuk starts and
writes its readiness record to the host journal, but Docker's journald log-reading
path returns no container log bytes. Testcontainers waits for that hidden record
and times out before broker tests begin. Docker's daemon/logging configuration
was repaired by the operator outside this code change. Ryuk/tests were not disabled.
After that repair, `mise run validate` passed, including all real-Mosquitto tests.

The Core startup-log timeout also occurred on that baseline. A separate uncached
current-tree Core package run passed; its exact intermittent cause remains
unproven. CI results must be evaluated separately from local evidence.

The first published CI run on PR #180 passed HomeKit, container smoke, web, and
fuzz checks but reproduced the Core startup-log timeout. The test now observes
early `Run` exits, reports completed stages, and joins its runtime before fixture
cleanup. Its original deadlines and assertions remain unchanged. This diagnostic
repair does not establish the intermittent startup failure's cause.

A subsequent CI run exposed a container-check assertion that incorrectly treated
characteristic array order as identity. The failure reproduced locally with
unchanged AID/IID/type bindings. HAP-NodeJS emits those array entries in async-read
completion order; the check now sorts identity tuples before comparing them,
without relaxing the required bindings. Only sanitized verdicts are retained on
CI failure; pairing data and raw container evidence stay private.

CI also exhausted a cancellation test's shared 30-second context during its
second Core startup. The restart now gets a separate 30-second startup context;
the test's independently measured 100 ms shutdown window remains unchanged.

## Cleanup

Stopped the production bridge and all four managed simulator daemons. Removed
the temporary worktree-local override and verified TCP 51826 no longer listens.
Private source/configuration, pairing storage, and evidence remain under
`/tmp/opencode/hearth-homekit-production-acceptance/`, not in the repository.
No firewall rules were added or changed in this production session. Removal of
the earlier disposable experiment's tagged firewall exceptions remains
unconfirmed. See [operator guidance](homekit.md) for setup and recovery.
