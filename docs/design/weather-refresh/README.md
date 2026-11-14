# Weather refresh verification

Weather cards previously fetched household and canonical representations
independently. During each refresh, one response could advance before the other.
The observation-match guard then removed condition words and inserted fallback
rows until the second response arrived.

One observation or aggregation supplies both representations, and the frontend
caches the complete pair together. The initial captures below used an opt-in
paired contract. The approved revision makes pairs the default for Entity-detail
GET and aggregate-history POST, removing both representation selectors from
those operations; other endpoints retain their existing format.

## Default-contract revision

The follow-up removes `include` and `units` from Entity-detail GET and numeric
aggregate-history POST. The default wire shape is the pair. OpenAPI publishes
only that representation, while collections, embedded Device Entities, PATCH
responses, audit history, and MCP retain their existing formats.

The frontend validates and caches a complete pair, then selects its canonical
or display projection locally. This applies to numeric and text cards, controls,
forecasts, and Entity detail. The detail page's raw inspector retains the paired
wire shape rather than displaying the normalized internal model.

The revision passed `mise run validate`: 773 frontend tests, 147 Chromium
stories, both web builds, generation, formatting, module tidying, lint, vet,
and Go unit/integration/E2E tests. Local Go tests were non-race, with normal
test caching for unchanged packages. Independent frontend and backend source
reviews found no remaining actionable defects.

A fresh isolated runtime used backend revision `3e78d25` plus the submitted
frontend changes, with the default metric configuration and no daemon override.
The browser used the production renderer and same-origin Core proxy:

- Plain GET responses contained canonical/display pairs for scalar, composite,
  and forecast Entities. Bucketed and unbucketed history POSTs also returned
  pairs without flags. The live OpenAPI document omitted both selectors.
  An Entity collection still returned its existing `state.value` shape.
- Published wind Observations matched Core's exact returned Observation IDs.
  [The desktop response](default-observation.json) records canonical and display
  6.3 m/s. [Desktop](default-desktop.png) showed “Moderate breeze”;
  [phone](default-phone.png) showed 4.5 m/s and “Gentle breeze” after a separate
  4.4704 m/s publication.
- Mutation observers measured zero missing-condition samples and **0 px**
  height or following-row movement at both 1440 × 1000 and 390 × 844.
  Phone had no horizontal overflow. See the
  [desktop](default-desktop-result.json) and [phone](default-phone-result.json)
  measurement receipts.
- The Entity detail inspector showed canonical 10000 mCel and display 10 Cel
  under the same State evidence, with no legacy `state.value` or top-level
  `quantities`. The separate audit table still showed canonical mCel.

The browser HAR recorded 70 detail/aggregate exchanges with no representation
flags. Six reads were cancelled during navigation; completed API exchanges
returned 200. Some large development-module bodies were truncated in the HAR;
the direct API receipts and completed detail body establish the checked shapes.
No Commands or model turns were sent. The owned simulator stack was stopped
and its daemon PIDs verified absent. Evidence remains under the task's
`/tmp/opencode/weather-jitter/default-live` directory.

The videos below remain evidence of the original jitter and its paired-read
fix, not captures of the follow-up default contract. The new screenshots and
receipts above cover that revision. Live rain transitions and a three-hour
pressure history remain covered by synthetic component/browser scenarios rather
than manufactured runtime history.

## Captures

Captured on November 14, 2026 in real Chrome, without model calls or physical
Device commands.

| Capture | Boundary | Result |
| --- | --- | --- |
| [Before video](before.mp4), [image](before.png) | Wanda's deployed Weather dashboard, asset `index-B_F1y1f9.js`, at 1440 × 1000 | The next normal refresh removed conditions and inserted fallback rows. The capture's mutation observer saw four missing-condition samples and up to 27 px of card-height and following-row movement. |
| [After desktop video](after-desktop.mp4), [image](after-desktop.png) | Worktree Core, native simulator, and production dashboard at 1440 × 1000 | A published 6.3 m/s Observation displayed as 14.1 mi/h with “Moderate breeze”. No missing-condition samples; maximum card-height and following-row movement were both 0 px. |
| [After phone video](after-phone.mp4), [image](after-phone.png) | The same production boundary at 390 × 844 | A published 4.4704 m/s Observation displayed as 10.0 mi/h with “Gentle breeze”. No missing-condition samples or horizontal overflow; both movement measurements were 0 px. |

The before and desktop-after media are cropped to the canvas to exclude sidebar
conversation history. Videos were transcoded to MP4 without audio or time edits.
The recordings were played through in Chrome and their frames inspected. The
before video includes initial loading and the subsequent refresh; its temporary
direction row is visible during that refresh.

The before and after data differ: Wanda supplies the real weather baseline;
after captures use explicitly published synthetic observations. These are
behavioral comparisons, not pixel-parity screenshots. The earlier investigation
at a wider viewport also measured a transient 74 px shift.

## Ordered regression evidence

Before production changes, this command failed while the canonical response
was deliberately held:

```sh
mise run --skip-deps web-test -- views-weather-cards.test.tsx \
  -t 'keeps the complete weather reading'
```

```text
AssertionError: expected '21.0mi/h' to be '14.1mi/h'
Expected: "14.1mi/h"
Received: "21.0mi/h"
```

The display had advanced while its formula input was still pending. The test
also checks that the old description and row arrangement remain visible while
waiting, then that the value and description update together.

After the fix, the focused frontend run passed:

```sh
mise run --skip-deps web-test -- \
  paired.test.ts views-weather-cards.test.tsx views-forecast.test.tsx
```

```text
Test Files  3 passed (3)
     Tests  89 passed (89)
```

The backend HTTP tests check one Entity lookup or aggregation, unchanged
observation evidence, scalar/composite/forecast/null-State shapes, US and metric
conversion, gaps, arbitrary metric aliases, output-size limits, invalid query
codes, published schemas, and unchanged legacy reads. Frontend boundary tests
reject incompatible units and incomplete pairs before replacing cached data.

The new `WeatherRefresh` and `PhoneWeatherRefresh` Storybook interactions also
observe every DOM mutation during a delayed refresh. They assert that fallback
rows never appear and every card's height and vertical position stay unchanged.
Focused MCP story tests passed for those stories and the four weather cards.

The final repository gate passed:

```sh
env -u GO_PACKAGES \
  PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/run/current-system/sw/bin/chromium \
  mise run validate
```

It included 763 frontend tests, 147 Chromium stories, both frontend builds,
generation, formatting, module tidying, lint, vet, and Go unit/integration/E2E
tests. Unchanged Go packages used normal test-cache results; local Go tests were
not race-instrumented. Storybook's manifest check verified 39 component APIs,
3 parameterless pages, and 147 story snippets.

Earlier full-gate attempts caught an over-complex contract-test loop and a
missing legacy `StateBody` OpenAPI component. The loop was simplified and the
schema component restored; neither failure was bypassed. Independent source
review also caught late history-unit validation and inconsistent malformed-query
error codes. Both were corrected and re-reviewed.

## Runtime provenance and limits

The after stack was launched from backend commit `443cbe2` plus this PR's working
changes. Its explicit, temporary Core CLI override selected US units. Shared
include-query validation was subsequently moved into its own file, and an unused
frontend test convenience export was removed; neither cleanup changed the
captured valid-request behavior. The complete submitted source is validated
separately by the repository gate.

The native simulator supplied input, but assertions used Core's paired HTTP
response and matched the exact returned Observation ID. The browser used the
same-origin production proxy, real refresh timers, and the production renderer.
The new scalar, thermostat, forecast, bucketed-history, and unbucketed-history
responses were also read through the running Core API.

No fix was deployed to Wanda. Live rain transitions and a three-hour pressure
history were not manufactured in the runtime stack; the rain and pressure
behavior is additionally covered by synthetic component and browser stories.
No physical-Device compatibility claim follows from these checks.
