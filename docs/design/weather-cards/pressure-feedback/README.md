# Pressure history refresh regression

Production Pressure cards and the shared read cache, with deterministic HTTP
fixtures in an isolated browser preview. No live Core or physical Device was
used. Captured November 9, 2026 from `a833ce8` plus the pressure-feedback fix
in this commit, at 1280×760 in metric units. Three cards share one pressure
Entity: no trend, a shared 6 h trend, and a separate 24 h trend.

1. [Healthy](healthy.png): successful State and history reads; Falling.
2. [Refresh failed](warning.png): history requests return 503 while State keeps
   refreshing; the last tendency remains temporarily, with a warning on every
   card, including the one without a trend.
3. [Expired](expired.png): advance browser timers by ten more minutes. No new
   history arrives, so the old tendency disappears. State is still current.
4. [Recovered](recovered.png): history requests succeed with a rising series;
   the warning disappears and the new tendency appears.

[Recording](refresh-expiry-recovery.mp4) shows the sequence. Browser time is
accelerated with Playwright's clock; eleven minutes elapse in seconds of video.
Network errors are deliberate fixture responses. The existing chart can keep
its last interval with its own warning after the tendency expires.

Regression tests drive the same successful load → failed refresh → expiry →
recovery through the production fetch/cache/binding path, with all three trend
settings. Before the fix all three cases failed because the warning was absent.
The tests now pass, including checks that State stays live and no Commands run.
