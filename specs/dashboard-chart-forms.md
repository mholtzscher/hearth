# Dashboard chart forms

- **Status:** Approved 2026-11-27 ("Implement it"). Implementation in progress.
- **Checkpoint:** Playbook: mstack spec implementation. Branch `feat/dashboards-chart-forms`, worktree `/home/michael/code/worktrees/hearth/proto-web-chart-forms`, base `origin/main` d5cf2e5. Delivery: one PR, one commit per step (the six-PR plan below would need a stack, which was not requested). Done on the branch: step 0 skeleton (`18cb437`); D1 and D2 frontend (`9848b92`: step line, `compare: "typical"`, editor option, `typical.ts` and `step.ts` with unit tests, two stories; Storybook history fixture now accepts any Go duration and serves held fields as steps). `web-test` 1276 passed, `time-series.stories.tsx` 7 passed. Running: three background workers in sibling worktrees `/home/michael/code/worktrees/hearth/feat-dashboards-chart-forms-{go,rank,week}` (go: Go side of D1–D7 and `examples.json`, session `ses_ddfedf49effeBVjs4h3X0SxbJ6`; rank: `Ranking` and `TimeInRange` cards, session `ses_ddfed8081ffeubKXHH3XIQj4r8`; week: `WeekGrid` and `PeriodTotals` cards, session `ses_ddfed19a1ffer1DHKEnKqYZEQj`). Next: review each handoff and diff, merge the three branches, remove story-local history overrides that the shared fixture now covers, D8 (DESIGN.md, `docs/reference/dashboards.md`, delete `web/src/features/views/prototypes/`), `mise run validate`, independent review, one PR, then delete this spec in that PR. Also still running from the prototype review: Storybook on port 6139 and `tailscale serve --https=6139` through a host-rewriting proxy (pid in the first session scratchpad).
- **Type:** Feature plan
- **Date:** 2026-11-27
- **Effort:** 7–9 focused engineering days, medium confidence. Counter totals (energy, rain) are excluded; see Open questions.
- **Prototype:** branch `proto/web-chart-forms`, commit `63f97d6`, stories under `Prototypes/Charts`. The takes chosen on 2026-11-27 are recorded in Proposed solution.

## Problem statement

**Who:** Household users who read dashboards, and the built-in Agent that authors them through `compose_view`.

**What:** The card catalog draws numeric history one way: a mean line with a min–max band (`TimeSeries`, and the trends on `Metric`, `Rain`, `Wind`, and `Pressure`). Five household questions have no card:

| Question | Why the line fails |
| --- | --- |
| When did the target change? | A sloped line between two samples claims the value passed through everything in between. |
| Is today unusual? | One window has nothing to compare against. |
| Which room is warmest? Which plug draws most? | Comparing current readings means reading every `Metric` or `ReadingList` row. |
| When is it usually warm? | `WeekPattern` shades only how long a yes/no field was true. |
| How much of the week was it in range? How long did the heater run each day? | Time order hides totals and shares. |

**Why it matters:** The Agent answers these today with a `TimeSeries` or a `ReadingList` and leaves the reader to work out the answer.

**Evidence:** The catalog in `web/src/features/views/catalog/index.ts` has 18 cards and one numeric-history form. The prototype review on 2026-11-27 kept all six prototyped forms.

## Proposed solution

Add four cards and extend `TimeSeries` in two ways. Every form uses the existing history and changes endpoints; Core gets no new query.

| Form | Chosen take | Shipped as |
| --- | --- | --- |
| Stepped line | Risers | `TimeSeries` draws held fields as a step line. No new key. |
| Period comparison | Against typical | `TimeSeries` option `compare: "typical"`. |
| Ranked readings | Both | New card `Ranking`. Hearth picks bars or the range plot. |
| Numeric heat grid | Blocks | New card `WeekGrid`. |
| Distribution | Bands | New card `TimeInRange`. |
| Period bars | Both | New card `PeriodTotals` with `style: "columns" \| "ledger"`, for on-time of a yes/no field only. |

Two rules hold across the forms:

- **The model names the question; Hearth picks the drawing.** The only style key is `PeriodTotals.style`, because both takes were kept. `Ranking` and the step line are decided from the data.
- **Every card leads with its answer in words**, as the prototypes do, and keeps the method in **Source**.

The six forms ship as separate pull requests in the order of the deliverables table. Each is usable alone.

## Scope and deliverables

| ID | Outcome | Effort | Owning paths | Depends on | Acceptance |
| --- | --- | --- | --- | --- | --- |
| D1 | `TimeSeries` draws a held field as a step line with a dot at each change. A series may name its own `field`, and one Entity may appear twice with different fields, so a target and a room temperature share a chart. | M | `web/src/features/views/cards/time-series/`, `web/src/features/views/bindings/state-fields.ts`, `internal/modules/dashboards/compose_view_cards.go`, `internal/modules/agent/compose_view.schema.json` | — | A1, A2 |
| D2 | `TimeSeries` with `compare: "typical"` draws today from local midnight over the last 7 days' range for each time of day. | L | `web/src/features/views/cards/time-series/`, `web/src/features/views/bindings/history.ts`, `internal/modules/dashboards/view_bindings.go`, `compose_view_cards.go`, `compose_view.schema.json` | — | A3, A4 |
| D3 | `Ranking` card: the current reading of 2–12 Entities, sorted, as bars from zero or a range plot. | L | `web/src/features/views/cards/ranking/` (new), catalog, renderer, editor, Go bindings and compose paths | — | A5, A6 |
| D4 | `WeekGrid` card: one numeric field as 7 days by 8 three-hour blocks, each block printing its mean. | M | `web/src/features/views/cards/week-grid/` (new), same shared paths | — | A7 |
| D5 | `TimeInRange` card: the share of a window a numeric field spent below, between, and above one or two thresholds. | L | `web/src/features/views/cards/time-in-range/` (new), same shared paths | — | A8, A9 |
| D6 | `PeriodTotals` card: on-time of a yes/no field per hour or per day, as columns or a ledger. | M | `web/src/features/views/cards/period-totals/` (new), `web/src/features/views/bindings/changes.ts`, same shared paths | — | A10 |
| D7 | `compose_view` accepts the new cards and options, `set_chart_window` reaches the new windowed cards, and evaluation scenarios cover one new form each. | M | `internal/modules/agent/compose_view.go`, `compose_view.schema.json`, `internal/modules/dashboards/edit_view.go`, `testdata/agenteval/scenarios/` | D1–D6 | A11, A12 |
| D8 | `DESIGN.md` and `docs/reference/dashboards.md` describe the new forms; the prototype folder is deleted. | S | `DESIGN.md`, `docs/reference/dashboards.md`, `web/src/features/views/prototypes/` (delete) | D1–D6 | A13 |

"Same shared paths" means: `web/src/features/views/catalog/index.ts`, `catalog/compatibility.ts`, `renderer/view-renderer.tsx`, `editor/view-editor.tsx`, the generated `contracts/views/v1/view.schema.json`, `internal/modules/dashboards/view.go`, `view_bindings.go`, and `compose_view_cards.go`.

## Non-goals

- **No counter totals.** Energy used per day and rain fallen per day need a per-period increase that min, max, and mean cannot give, and Core has no way to know a field is a running counter. `docs/reference/units.md` already states that counter summaries are not totals. See Open questions.
- No new Core endpoint, history operation, or bucket alignment.
- No exact change times for numeric fields. `POST /v1/entities/{id}/state/changes` serves yes/no and text fields only, so a step lands on a bucket edge.
- No time-weighted statistics. `TimeInRange` and `WeekGrid` use sample means, as `TimeSeries` does.
- No `yesterday` comparison, no histogram, no hourly heat grid, and no "holds" step style. Those takes were not kept.
- No pie chart, radial gauge, stacked area, or scatter plot.
- No change to `Metric`, `StateTimeline`, `WeekPattern`, or the weather cards.

## Types

### Portable catalog (zod, the source of `contracts/views/v1/view.schema.json`)

`TimeSeries` gains one optional key. The step rendering adds none.

```diff
diff --git a/web/src/features/views/cards/time-series/schema.ts b/web/src/features/views/cards/time-series/schema.ts
@@
 export const timeSeriesSchema = z.strictObject({
   title: plainText(120),
   series: z.array(z.strictObject({ source: seriesBindingSchema, label: plainText(120).optional() })).min(1).max(8),
+  compare: z
+    .literal("typical")
+    .describe(
+      "Draw today from local midnight over the last 7 days' range for each time of day. Requires exactly one series with window 24h."
+    )
+    .optional(),
 });
```

New cards, each in its own `schema.ts`:

```ts
// cards/ranking/schema.ts
export const rankingSchema = z.strictObject({
  title: plainText(120),
  rows: z
    .array(z.strictObject({ source: stateBindingSchema, label: plainText(120).optional() }))
    .min(2)
    .max(12)
    .describe("Distinct numeric State fields in one canonical unit."),
  range: z
    .enum(seriesWindows)
    .describe("Also show each row's minimum and maximum over this window.")
    .optional(),
});

// cards/week-grid/schema.ts
export const weekGridSchema = z.strictObject({
  source: stateBindingSchema.describe("A numeric State field."),
  label: plainText(120).optional(),
});

// cards/time-in-range/schema.ts
export const timeInRangeSchema = z.strictObject({
  source: stateBindingSchema.describe("A numeric State field."),
  label: plainText(120).optional(),
  window: z.enum(seriesWindows),
  thresholds: z
    .array(z.number())
    .min(1)
    .max(2)
    .describe(
      "One or two ascending thresholds in the field's canonical unit. One splits the window into below and above; two add the range between them."
    ),
});

// cards/period-totals/schema.ts
export const periodTotalsStyles = ["columns", "ledger"] as const;
export const periodTotalsSchema = z.strictObject({
  source: textBindingSchema.describe("A yes/no State field. Text fields are rejected."),
  label: plainText(120).optional(),
  words: wordsSchema.optional(),
  period: z.enum(["hour", "day"]).describe("hour: the last 24 hours. day: the last 7 days."),
  style: z
    .enum(periodTotalsStyles)
    .describe("columns: one labelled bar per period. ledger: one row per period, read like a table."),
});
```

`stateBindingSchema`, `textBindingSchema`, `wordsSchema`, and `seriesWindows` are the existing exports of `web/src/features/views/bindings/schema.ts`.

### Held fields (frontend only)

A held field holds its last value until the next change. The list lives beside the field catalog and drives the step rendering:

```ts
// bindings/state-fields.ts
/** Fields that are set, not measured: drawn as a step line. */
export const isHeldField = (entityType: string, field: string): boolean =>
  entityType === "hearth.numericsetting/v1" ||
  entityType === "hearth.brightness/v1" ||
  (entityType === "hearth.thermostat/v1" && field.startsWith("/target_"));
```

### Ranking mark rule (frontend only)

`Ranking` draws the range plot when any of these holds, and bars from zero otherwise:

1. `range` is set.
2. The field's `quantity_kind` is `temperature`, `atmospheric_pressure`, `color_temperature`, `color_hue`, or `chromaticity`: zero is not "none" for these.
3. Any current value is negative.

### Authoring input (`compose_view`, model-visible)

Notation follows `specs/unified-compose-schema.md`.

```diff
-TimeSeries = { type: "TimeSeries", title, series: EntityLabel[1..8], field?: Field, window: Window }
+SeriesItem = { entity_id: EntityID, field?: Field, label?: Label }
+TimeSeries = { type: "TimeSeries", title, series: SeriesItem[1..8], field?: Field, window: Window, compare?: "typical" }
+Ranking = { type: "Ranking", title, rows: SeriesItem[2..12], range?: Window }
+WeekGrid = { type: "WeekGrid", entity_id: EntityID, field?: Field, label?: Label }
+TimeInRange = { type: "TimeInRange", entity_id: EntityID, field?: Field, label?: Label, window: Window, thresholds: number[1..2] }
+PeriodTotals = { type: "PeriodTotals", entity_id: EntityID, field?: Field, label?: Label, words?: Words, period: "hour" | "day", style: "columns" | "ledger" }
```

- A series item's `field` overrides the card's `field`. Series must share one canonical unit; they no longer need one Entity type, and `(entity_id, field)` pairs must be distinct, not Entity IDs.
- `TimeInRange.thresholds` are in the household's display unit. Hearth converts them to the canonical unit when it compiles the card.

## Interfaces

### Data reads

No endpoint changes. New read requests reuse the two existing queries with different ranges and bucket lengths:

| Card | Endpoint | Range | Bucket | Folded in the browser into |
| --- | --- | --- | --- | --- |
| `TimeSeries` compare, today | `state/history` | local midnight to now | `5m` | the today line |
| `TimeSeries` compare, typical | `state/history` | 7 local days before today's midnight | `30m` (336 buckets) | per time of day: min, max, and mean of the 7 bucket means |
| `Ranking` | current State; `state/history` when `range` is set | the window | one bucket spanning the window | each row's min and max |
| `WeekGrid` | `state/history` | 6 local days before today's midnight, to now | `3h` (at most 56 buckets) | one cell per bucket |
| `TimeInRange` | `state/history` | the window | the window's existing bucket | counts of bucket means per band |
| `PeriodTotals` | `state/changes` summary | `hour`: last 24 hours; `day`: 6 local days before today's midnight, to now | `1h` | seconds true per period |

All bucket counts stay under `HistoryMaxBuckets` (1000). `alignment` stays `range_start`; ranges that start at local midnight keep buckets on local clock times except across a daylight-saving change (see Risks).

```ts
// bindings/history.ts — the existing window reads keep their signature
export const historyRangeRequest = (
  source: NumericSource,
  range: { from: Date; bucket: string; count: number },
  canonicalUnit?: string
): ReadRequest;
```

### Card states

Each new card follows the Card states table in `DESIGN.md`. Specific to these forms:

- A period, block, or band with no samples shows `—` or "No history", never zero.
- The current, incomplete period is tinted and outlined and labelled "so far".
- Future blocks in `WeekGrid` are outlined, as in `WeekPattern`.
- `TimeSeries` with `compare` and no usual range yet (fewer than 2 earlier days with samples) draws today alone with the note "Not enough history to compare."
- An unavailable or disabled `Ranking` row keeps its last value in `gray-8` with a hollow mark and its status words.

### Go validation and compilation

```diff
diff --git a/internal/modules/dashboards/view_bindings.go b/internal/modules/dashboards/view_bindings.go
@@ func collectBindings(raw json.RawMessage) ([]binding, error)
+	case rankingNode:
+		return rankingBindings(props)      // 2–12 numeric fields, one canonical unit, distinct (entity, field)
+	case weekGridNode:
+		return weekGridBindings(props)     // one numeric field
+	case timeInRangeNode:
+		return timeInRangeBindings(props)  // one numeric field; thresholds ascending
+	case periodTotalsNode:
+		return periodTotalsBindings(props) // one Boolean field
```

- `seriesBindings` additionally rejects `compare` unless the card has exactly one series whose window is `24h`.
- Rejections use the existing `DefinitionError` and `ViewArgumentError` paths, so `compose_view` returns `invalid_definition` with a path and never echoes input.
- `EditView`'s `set_chart_window` accepts `TimeInRange` and `Ranking` (its `range`) as well as `TimeSeries`. It rejects a `TimeSeries` that has `compare`, because that card's window is fixed.

## Project layout

```text
web/src/features/views/
├── bindings/
│   ├── history.ts                 # modify — range-based history read beside the window reads
│   ├── changes.ts                 # modify — per-period on-time read for PeriodTotals
│   └── state-fields.ts            # modify — isHeldField
├── cards/
│   ├── time-series/               # modify — step rendering, compare: "typical", editor option
│   ├── ranking/                   # new — schema, card, editor, stories
│   ├── week-grid/                 # new — schema, card, editor, stories
│   ├── time-in-range/             # new — schema, card, editor, stories
│   └── period-totals/             # new — schema, card (columns and ledger), editor, stories
├── catalog/index.ts               # modify — four new element schemas
├── catalog/compatibility.ts       # modify — source compatibility for the new cards
├── renderer/view-renderer.tsx     # modify — render the new cards
├── editor/view-editor.tsx         # modify — add and edit the new cards
└── prototypes/                    # delete — replaced by the shipped cards
contracts/views/v1/
├── view.schema.json               # modify — regenerated by `mise run --skip-deps generate`
└── examples.json                  # modify — one example per new card
internal/modules/dashboards/
├── view.go                        # modify — node type constants
├── view_bindings.go               # modify — binding collectors and checks above
├── compose_view_cards.go          # modify — compilers for the new authoring shapes
└── edit_view.go                   # modify — set_chart_window targets
internal/modules/agent/
├── compose_view.schema.json       # modify — authoring shapes above
└── compose_view.go                # modify — tool description names the new cards
testdata/agenteval/scenarios/      # new — one scenario per new form
DESIGN.md                          # modify — Charts: bars, steps, blocks, bands
docs/reference/dashboards.md       # modify — card catalog entries
```

Each card's rendering code starts from its prototype in `web/src/features/views/prototypes/charts/`, with fixtures replaced by bound reads.

## Acceptance criteria

- [ ] **A1:** A `TimeSeries` of a thermostat `/target_temperature` draws horizontal holds joined by vertical risers, with a dot at each change; a `hearth.temperature/v1` series in the same chart still draws a sloped mean line with its band.
- [ ] **A2:** `compose_view` accepts one thermostat Entity twice in `series` with `/target_temperature` and `/current_temperature`, and rejects two series with different canonical units.
- [ ] **A3:** A `TimeSeries` with `compare: "typical"` shows today's line ending at the latest bucket on a midnight-to-midnight axis, a band, a legend naming "Usual range, last 7 days", and a sentence that states whether today is above, below, or within the usual range.
- [ ] **A4:** `compare` with two series, or with a window other than `24h`, is rejected when a view is presented or saved.
- [ ] **A5:** A `Ranking` of power readings sorts highest first and draws bars from zero; the same card bound to temperatures draws the range plot. Neither needs a key from the author.
- [ ] **A6:** A `Ranking` row whose Entity is unavailable keeps its last value, dimmed, with "Unavailable" in words.
- [ ] **A7:** A `WeekGrid` prints a mean in each three-hour block that has samples, dashes blocks without samples, outlines future blocks, and at 390px prints whole numbers without horizontal overflow.
- [ ] **A8:** A `TimeInRange` with thresholds 20 and 22 °C shows three named ranges whose hours sum to the hours with samples and whose percentages sum to 100 within rounding.
- [ ] **A9:** A `TimeInRange` saved in °C shows the same ranges, relabelled, when the household switches to °F.
- [ ] **A10:** A `PeriodTotals` with `period: "day"` shows seven periods, marks today "so far", shows "No history" for a day without data, and renders in both styles.
- [ ] **A11:** `compose_view` creates a view containing each new card from the authoring shapes above, and `set_chart_window` changes a `TimeInRange` window without altering any other property.
- [ ] **A12:** Each new evaluation scenario passes its reference trajectory offline.
- [ ] **A13:** `DESIGN.md` and `docs/reference/dashboards.md` describe every shipped form, and `web/src/features/views/prototypes/` no longer exists.

## Validation plan

| Acceptance | Check | Expected result | How to run | Prerequisites |
| --- | --- | --- | --- | --- |
| A1, A3, A5–A8, A10 | Stories for each card state, with play assertions and axe checks, at desktop and phone widths | All pass; phone stories have no horizontal overflow | `mise run --skip-deps storybook-test -- <card>.stories.tsx` | Chromium, per `docs/guides/storybook.md` |
| A1, A3, A5, A8, A10 | Unit tests of the folds: step points, typical band, ranking mark rule, band counts, period sums | Pass | `mise run --skip-deps web-test` | None |
| A2, A4, A11 | Go tests of binding checks, compilers, and `EditView` | Pass | `GO_PACKAGES='./internal/modules/dashboards ./internal/modules/agent' mise run test` | None |
| A9 | Story that switches the unit system for a saved `TimeInRange` | Labels change, shares do not | `mise run --skip-deps storybook-test -- time-in-range.stories.tsx` | Chromium |
| A11 | End-to-end dashboards journey presents and saves a view with each new card | Pass | `GO_PACKAGES='./internal/app/hearthd' mise run test -- -tags=integration,e2e` | None |
| A12 | Offline replay of the reference trajectories | Pass | Command per `docs/guides/agent-evaluation.md`; confirm during D7 | None offline; a hosted run needs model credentials |
| A13 | Generated contract and docs are current | No diff after regeneration; docs name each card | `mise run --skip-deps generate`, then `git diff --exit-code contracts/` | None |
| All | Completion gate | Pass | `mise run validate` | Docker for the real-Mosquitto tests |

## Risks and mitigations

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Six more shapes lower the Agent's first-try accuracy on `compose_view`. | Medium | Rejected attempts cost about 4.5 s each. | One spelling per key, per `specs/unified-compose-schema.md`; D7 adds a scenario per form and reports a comparison against `main`. |
| A step drawn from bucket means is up to one bucket late (1 to 30 minutes) and the bucket holding a change shows an in-between value. | High | A wrong change time on 7-day charts. | Treat a bucket whose min and max differ as the change bucket and draw the riser at its start; state the bucket length in Source. Exact times are a non-goal. |
| On the two daylight-saving days a year, `range_start` buckets drift one hour from local clock times. | Certain, twice a year | Typical band and week blocks are one hour off for up to 7 days. | Fold by each bucket's own local time, not its index; cover with a unit test on a DST week. |
| Thresholds stored in canonical units read oddly after a unit-system change (20 °C becomes 68 °F, 22 °C becomes 71.6 °F). | Medium | Awkward range labels. | Format labels with the field's display digits; accepted. |
| Sample means differ from time-weighted shares for sensors that report on change. | Medium | `TimeInRange` over- or under-counts by a few percent. | State the method in Source, as `TimeSeries` does; time-weighting is a non-goal. |

## Trade-offs made

| Chose | Over | Because |
| --- | --- | --- |
| New cards for the heat grid and totals | Widening `WeekPattern` to numeric fields | Its source and `words` are yes/no shaped; a union on overlapping Entity types would make both the contract and the model's choice ambiguous. |
| Hearth picks bars or range plot | A `style` key on `Ranking` | The choice follows from the quantity, and authoring trials lost accuracy on every extra key. |
| A calendar-day axis for `compare` | The rolling 24-hour window | It matches the approved prototype and the question "is today unusual?". |
| On-time totals only | Waiting for counter totals | It ships the bar form now, with no Core change. |
| Thresholds in canonical units in the saved definition | Display units | A saved dashboard must mean the same thing under either unit system. |

## Open questions

- [ ] Should counter totals (energy and rain per day) get their own spec? They need a way for Core to know a field is a running counter and a per-period increase in the rollups. → Owner: Michael. Non-blocking: `PeriodTotals` ships for on-time without it.
- [ ] Should `PeriodTotals` default to one style when the model omits it? This spec requires `style`. → Owner: Michael. Non-blocking.

## Success metrics

- Each of the five questions in the problem statement has a card that states its answer in words.
- `compose_view` first-try pass rate on the existing `dashboard` scenarios does not drop against `main`.
- No new Core endpoint or migration ships with this spec.
