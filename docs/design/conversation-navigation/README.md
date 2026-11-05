# Conversation navigation evidence

These captures use real Core and Vite in a worktree-local simulator stack.
Only Devices are simulated. Agent replies use the configured OpenAI provider.
There are no reconstructed frames or redactions.

## Before

Baseline: `682e5527e7f41df564cd52c5bf98b4a641f2a3af`, 1440 × 900.

- `before-running.png`: a new turn is running, but New conversation is disabled
  and the browser is still at `#/`.
- `before-returned.png`: leave for Dashboards during the reply, then return
  through Recent. Only the user message remains. An independent Core history
  read contains one user row and no assistant reply.

## After

The after captures use the navigation implementation committed with these files.
Viewport: 1280 × 800.

- `after-running.png`: the new conversation has its canonical URL and a Recent
  entry while Hearth is still working. New conversation is enabled.
- `after-returned.png`: after starting a new conversation and visiting Dashboards,
  returning through Recent shows the completed reply.
- `navigation.mp4`: the same journey, recorded at 10 fps for 11.7 seconds.
  Navigation occurs rapidly at the start; the rest waits for the real reply.
  The original capture is unedited. The video decodes successfully and its
  captured state sequence was inspected with the recorder's contact sheet.

HAR inspection found exactly one create POST and one message-stream POST for
the recorded journey, both HTTP 200. Core history independently contains the
user message, tool results, and completed assistant reply.

## Other verification

Browser checks also covered leaving and returning to a running conversation,
completion while staying on Dashboards, and a background view leaving a different
composer's unsent text and canvas untouched. Frontend tests cover concurrent
conversations, failed creation and history recovery, backend switching,
reattached canvas updates, delayed-create ownership handoff, preservation of both
prompts after a failed handoff, and removal of acknowledged Recent placeholders.
The delayed-create race and recovery failures use controlled HTTP responses in
frontend tests, not fault injection into the real provider.

`mise run validate` passes with 299 web tests. Raw browser snapshots, HARs,
history reads, and the local task checkpoint are retained outside tracked source
in `.data/verification/20261105T204658Z-navigation/` and
`specs/conversation-background-turns.md`.
