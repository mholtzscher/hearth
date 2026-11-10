# Outdoor temperature history refresh regression

Production Outside now cards and the shared read cache with deterministic HTTP
fixtures, not live Core or physical Devices. Captured November 9, 2026 from
`5637202` plus this commit's Outside now fix. Desktop: 1120×700, with metric
and US cards. Phone: 390×780.

| Capture | Result |
| --- | --- |
| [Healthy](healthy.png) | History loaded; normal 24 h high and low |
| [Refresh failed](warning.png) | History returns 503 while current State rises to 20 °C / 68 °F; cached extrema are labelled Last loaded 24 h with a warning |
| [Source](source.png) | Exact start and end of the retained history interval, including date, seconds and timezone |
| [Recovered](recovered.png) | History succeeds again; warning and cached-interval label disappear, updated extrema appear |
| [Phone Source](phone-source.png) | History timestamps in the existing bottom sheet; no horizontal overflow |

Browser time was accelerated by six minutes for the failed-refresh state.
Screenshots cover the changed output. A video was attempted but finalization
timed out; no video is attached. Synthetic network failures were deliberate.

Metric and US regression tests drive the production fetch/cache/binding path
through successful load → history failure with live State → recovery. They
check that Source preserves the cached interval until a successful refresh,
the current temperature still updates, and no Commands are sent.
