# Screen space evidence

Before screenshots use `5d208f2`; after screenshots use `3e6f1b3`. Both sets were captured
against the same worktree-local simulator stack and the saved
"House Climate" dashboard: three readings in a three-column grid and a
two-series chart. No physical devices or hosted model calls were used.

Each set follows the same steps in a fresh browser session:

1. Open the dashboard at 1440×900, change its title in the editor, and
   close the editor. The edit stays in the browser and is discarded later.
   Capture `dashboard-unsaved-1440` and `phone-dashboard-unsaved` (390×844).
2. Expand the canvas at 1920×1080 and capture `expanded-1920`.
3. Discard the edit, choose **Ask Hearth to change**, and capture the home
   split at 1280×800 (`split-1280`) and 1024×768 (`split-1024`).
4. At 390×844, open the Canvas tab and capture `phone-canvas`.
5. Open Development › Entities at 1440×900 and capture `development-1440`.

## Measurements

Measured from element boxes in the same runs, in CSS pixels.

| Check | Before | After |
|---|---|---|
| Phone canvas, saved dashboard: first content | y=340 | y=276 |
| Phone dashboard page, saved: first content | y=287 | y=223 |
| Canvas width in the home split at 1024px | 406px | 552px |
| Expanded canvas content at 1920px | 1080px | 1440px |
| Dashboard page content at 1920px | 1080px | 1440px |
| Composer with a selected view at 1440px | 129px | 106px |
| Development header at 1440px | 85px | 49px |

## Known gaps

- At 1024px, a three-column grid of reading cards still clips a long unit
  such as "hPa". The clipping is worse before this change; card overflow
  in narrow grids is a separate problem.
- The 44px coarse-pointer targets were not checked on a physical touch
  device.
