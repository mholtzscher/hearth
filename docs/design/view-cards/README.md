# New dashboard cards evidence

Captured on 2026-11-06 from `feat/web-new-view-cards`, based on `5d208f2`, with
the implementation committed alongside this evidence. Core and the web UI are
real; Devices are the worktree-local scripted simulator. No model calls or
physical Device Commands were sent.

- [Desktop, 1440 × 1000](desktop.png): State words, Metric trends and ranges,
  mixed ReadingList rows, a light composite, and multisensor Device rows.
- [Phone, 360 × 900](phone.png): list and Device rows with the oldest-reading
  footer. Also checked at 400px without horizontal overflow.
- [Edit and save](edit-and-save.mp4): opens the State editor, changes the true
  word, and saves revision 3. The native recording's eight captured frames were
  slowed 16× for readability; it is not evidence of execution timing. The
  original video, HAR, API responses, and additional captures remain in
  `.data/verification/new-view-cards/`.

These card types have no equivalent production before state. The approved
prototype is retained at `.data/design/view-cards/index.html`. Fresh simulator
history intentionally leaves most of each trend window empty; gaps are not
filled with invented samples.

The first browser journey saved one State-word edit as revision 2; the HAR
contains one corresponding successful PUT and an independent Core read confirms
the retained words. A simulator Observation changed the occupancy State. Its
returned observation ID matches Core, and the UI showed the new words with an
arrival glow. The same run was restarted for the recorded editor journey;
its second HAR contains one successful PUT, independently confirmed as revision 3.
An initial blank page after the restart had pending Vite module requests;
reloading rendered normally. Both browser sessions and all owned daemons were
stopped. Storage and local evidence remain intact.

Headless Chromium only: physical touch devices, Safari, conversation-embedded
rendering, and hosted-model card selection were not exercised. Native
agent-browser 0.41.2 used the already-installed 0.39.0 guide, with command help
confirming the operations used. The design detector returned zero findings.
