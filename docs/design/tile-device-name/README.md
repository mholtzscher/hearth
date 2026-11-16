# Device names on power tiles

An unlabeled ToggleTile uses its owning Device's display name instead of the
Entity name, which can be the generic "Power". Explicit tile labels still take
precedence. If the Device read fails, the Entity name remains the fallback.
Layout, controls, observation freshness, and Source are unchanged.

## Capture conditions

- Before: production component from `a934319277b0f9cd0b6a7e98c4e198de31a96f93`,
  extracted into a disposable baseline directory.
- After: this change's production component, captured before committing.
- Both use the same synthetic HTTP fixture: Device "Office plug", Entity
  "Power", power off, and the fixed Storybook clock `2026-11-09T10:00:30Z`.
  These captures establish component rendering, not physical Device behavior.
- Chromium, reduced motion, desktop 1200 × 800 and phone 390 × 844.
- Screenshots are unedited and contain no household data.

| Viewport | Before | After |
| --- | --- | --- |
| Desktop | [Generic Entity heading](before-desktop.png) | [Device heading](after-desktop.png) |
| Phone | [Generic Entity heading](before-phone.png) | [Device heading](after-phone.png) |

## Verification

- The focused regression failed before the change: expected article "Desk lamp",
  but the rendered article was "Power".
- All 29 ToggleTile/ToggleList component tests pass. New coverage checks the
  Device heading, the original Entity-targeted Command, custom-label precedence
  without a Device read, and usable control after a failed Device read.
- All seven ToggleTile stories pass through the worktree-local Storybook MCP
  test tool, including the new `DeviceName` story.
- Full validation passed using
  `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/run/current-system/sw/bin/chromium mise run validate`:
  1054 frontend unit tests, 286 Storybook tests, frontend/Storybook builds,
  manifest checks, lifecycle tests, and the repository's Go validation gate.
  Earlier attempts were blocked by missing Playwright Chromium, then its
  unsupported Linux build's missing `libglib-2.0.so.0`. The successful run uses
  installed NixOS Chromium without changing shared configuration.
- Browser checks confirmed no horizontal overflow at both capture widths,
  Source opens and restores focus on Escape, and Space still switches the
  synthetic Entity while retaining the Device heading.
- Impeccable found no issues in the changed production component.
- The disposable baseline server was stopped. The implementation's Storybook
  preview remains available for inspection.
