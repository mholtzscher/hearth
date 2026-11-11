# Responsive card sizing

These captures compare `00b0ac4` with the responsive-card changes on
`fix/web-card-sizing`, based on that revision. The after captures include the
final UI changes; subsequent edits affect tests and documentation only.

The real Vite application uses worktree-local Core and simulated Devices.
Five definitions passed Core's dashboard write validation. Browser interception
supplies conversation history so the review needs no hosted model call.
Readings and history come from Core. For the dense-forecast pass, browser-only
fixtures repeat the simulator's six hourly and three daily periods into 48 hours
and eight days. The views request 24 hours and five days. Those predictions are
synthetic layout fixtures, not weather data or proof of forecast ingestion.

## Before and after

| Capture | Viewport | Observed result |
| --- | --- | --- |
| [Phone temperatures, before](before-phone-temperatures.png) | 390 × 844 | One 306px-wide card per row inside another frame |
| [Phone temperatures, after](after-phone-temperatures.png) | 390 × 844 | Two 164px-wide cards per row; compact time and Source do not overlap |
| [Desktop hourly, before](before-desktop-hourly.png) | 1920 × 1080 | Chart scales text and icons with its width; card grows to 1,238px |
| [Desktop hourly, after](after-desktop-hourly.png) | 1920 × 1080 | Dense 24-hour plot stays 216px high; card is about 353px |
| [Phone weather](after-phone-weather.png) | 390 × 844 | Outside, Wind, and Pressure retain full-width layouts |
| [Phone hourly](after-phone-hourly.png) | 390 × 900 | Dense forecast scrolls horizontally instead of increasing its height |
| [Desktop weather](after-desktop-weather.png) | 1280 × 900 | Three weather cards share a row in the conversation |
| [Long numeric reading](long-number-phone.png) | 390 × 844 | `987654321.5 W.h` wraps within a 164px card without losing digits |

The temperature view contains six Metrics with 24-hour trends and one chart.
Its measured height drops from 2,202px to 1,269px at 390px, about 42%.
Readings advance naturally between captures. The hourly before capture has six
periods; the after capture deliberately exercises 24. They demonstrate the same
scaling defect and correction, not identical weather content.

## Layout and interaction checks

[Measurements](measurements.json) record the five conversation views at widths
320, 390, 768, 1024, 1280, and 1920px. Every width has zero document overflow,
zero collisions between time text and Source, and zero clipped card bodies
outside intentional chart scrollers. Weather includes Outside, Wind with and
without a trend, Pressure, and hourly and daily Forecast cards. Other views cover
Metric, quick switches, Device controls, ToggleList, ReadingList, and metering.
At 320px, compact grids correctly return to one column.

Source occupies a 28px row and has a 44px press target on phones. Clicking 6px
above or below its visible box opens details without changing the tile's power
State. Enter opens the sheet; Escape and Close return focus to Source. The
desktop popover also opens with Enter, returns focus after Escape, and leaves
the card height unchanged.

The [Source interaction video](source-target.mp4) shows the compact footer,
details sheet, and restored focus in the real application. Capture uses a
390 × 844 viewport, CDP recording at 20 fps, and no audio. The output is
374 × 844 after the recorder's own sizing. The first 7.5 seconds were trimmed
to remove stale startup frames; there are no other edits or redactions.
Decoded frames were inspected throughout the clip.

## Validation and limits

- `mise run validate` passed, including 646 frontend tests in 64 files,
  TypeScript/build, Go tests, generation, formatting, module tidy, lint, and vet.
- Regression tests exercise observer resize and cleanup, the 48-hour minimum
  chart width, grid classification, and the inline view action.
- The Impeccable detector reported no findings on changed production UI files.
- Independent review found a missing test prop and possible long-number
  clipping; both were corrected and revalidated.
- The first full validation attempt failed because the test used `Array.at`,
  beyond this project's configured TypeScript library. Indexed access fixes it;
  the complete validation retry passed.

Evidence uses resized Chromium viewports and mouse/keyboard events. Physical
iOS/Android devices, Safari, touch scrolling, and slider gestures were not tested.
The browser review does not establish model selection quality. Full frontend
tests cover Rain behavior, but Rain was not included in the browser fixtures.
The simulator stack and browser are task-owned; main-checkout data is untouched.
