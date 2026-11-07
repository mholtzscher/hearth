# Automation cards verification

These captures show production Core and the web app with worktree-local simulated
Devices. They contain no household data. No physical Adapter was started.

## Capture provenance

- Baseline UI: `908258d`, run from the catalog-only worktree at `f90ecae`.
  Its unused generated catalog does not change the baseline UI.
- Read cards and conversation: `feca56a`. Builder captures and video: `edeea5a`.
  The latter adds visible Step context to the action groups; read cards are unchanged.
- Date: 2026-11-07 UTC. Chromium 156 through agent-browser 0.41.2.
- Desktop: 1440 × 1000. Phone: 390 × 844. Split workspace: 1180 × 900.
  The editor also passed a 320px field-overflow check.
- Screenshots use reduced motion. The nested-builder capture is intentionally
  scrolled to the inner child Step. Conversation shows a historical replacement
  proposal selected again, before another Save; its original base remains revision 1.

| Files | State |
|---|---|
| `before-desktop.png`, `before-phone.png` | Baseline detail with the same saved revision 9 |
| `desktop-sentence.png`, `desktop-steps.png`, `phone-sentence.png` | Two read styles over the nested definition |
| `desktop-builder.png`, `phone-builder-header.png`, `phone-nested-builder.png` | Stack builder, including child and ancestor action context |
| `narrow-canvas.png`, `conversation-proposal.png` | Selected definition and replacement proposal in the shared workspace |
| `authoring.webm` | Real 6.7-second capture: switch style, inspect child, enter invalid delay, observe blocked Save, and restore the original value |

The video is a browser recording, not an animation or reconstructed screen.
Earlier browser submissions verified successful Saves against Core GET responses.

## Observed outcomes

- Created child and parent through structured forms, including a compound
  Condition, nested If, multi-Step Then/Else, and two child references.
- Reloaded canonical definitions, retained a draft after 409, then explicitly
  reloaded and replaced it. Large integer, decimal, and exponent tokens survived
  sibling edits through the final revision 9 request and authoritative GET.
- One manual parent Run succeeded. Two child Runs retained matching invocation
  causes and parent Step links. Inspection did not start another Run.
- A real model presented saved-reference, create, and replacement artifacts.
  Create and replacement committed only after browser Save. Reload showed one
  replacement artifact, with its original tool-call identity.
- The browser HAR contains three creations, one manual Run, one conflicting PUT,
  seven successful parent PUTs, and one successful proposal replacement PUT.
  The final capture-only HAR contains no mutations. Raw HARs remain outside Git.
- Child preview closure restored visible focus through collapsed ancestors.
  Browser errors were empty. The detector returned no findings.
- New heading-order findings were corrected. Axe still reports the baseline's
  three duplicate/nested-main landmark findings on Development pages; this change
  does not repair the existing application shell.

## Model limitation

One live replacement turn emitted rounded decimal/exponent literals in its tool
arguments. The tool and artifact retained those submitted values; that proposal
was discarded without saving. Browser editing and context transport preserve
exact tokens, but they cannot guarantee a model reproduces every literal.
The simple create/replacement journeys passed after schema guidance was improved.
Two intervening model requests failed before any tool call; their prompts remained
recoverable. These failures are not counted as successful journeys.

All owned browsers, the simulator stack, and the baseline frontend were stopped.
SQLite, JetStream, and private proof artifacts were retained locally.
