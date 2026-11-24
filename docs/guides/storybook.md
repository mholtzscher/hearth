# Develop the dashboard with Storybook

Storybook runs the production React components with Hearth's dark theme and
synthetic Core HTTP responses. Core, NATS, model credentials, and physical devices
are not needed. These stories verify browser component behavior, not backend
semantics or device compatibility.

Paths beginning `.storybook/` or `src/`, and `.pnpmfile.cjs`, are relative to
`web/`. Other paths and all commands in this guide are relative to the repository
root.

## Set up once

```sh
mise run web-install
```

If neither `chromium` nor `chromium-browser` is executable on `PATH`, install
Playwright's bundled Chromium once:

```sh
mise run storybook-browser-install
```

Managed Storybook and the test commands use the same browser selection:

1. `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`, if set.
2. The first executable `chromium` or `chromium-browser` on `PATH`.
3. Playwright's bundled Chromium.

No OS detection or environment export is needed. System Chromium can differ
from Playwright's bundled version; browser compatibility depends on that version.

## Start and stop

```sh
mise run storybook-start
mise run storybook-stop
```

Start uses worktree-local mise/pitchfork supervision and waits up to 45 seconds
for a nonempty story index. It prints the local URL, MCP URL, MCP configuration
path, and log path. Repeating the command reuses the daemon. No installation or
tests run during startup.

Each checkout gets its own port from mise. The primary checkout uses 6006 and a
linked worktree uses a port derived from its path. Storybook binds `127.0.0.1`
only; the managed launcher does not configure a tunnel. Use your own tunnel if
you need to reach it from another device.

Start writes this checkout's MCP endpoint to the ignored
`.opencode/opencode.json`. OpenCode watches that file and reloads the
`hearth-storybook` server. Do not add the server with `opencode mcp add` or
hardcode a port. OpenCode reloads the file and connects asynchronously, so the
first `opencode mcp list` after start can omit the server or show it as
`pending`. Repeat the command until it shows `hearth-storybook` as connected.
If it stays absent, run `opencode debug config` in the same checkout to check
that OpenCode found the generated file. Stop removes the
generated file because the endpoint no longer exists. Storybook's MCP and
component manifest features are upstream preview features.

Occupied ports fail without replacing services or stopping unrelated processes.
Stop the process that holds the port, or redeclare `[daemons.storybook]` with a
fixed `port` in a local `mise.local.toml`, repeating its other keys. Run
`storybook-stop` before changing the port configuration.

Logs live in ignored `.data/storybook/`. Use `mise daemons status storybook` to
inspect the daemon.

Foreground usage remains `mise run storybook`, stopped with Ctrl+C. It uses the
same assigned port but does not manage the MCP configuration; use
`storybook-start` when an agent needs the server. Run lifecycle tests with
`mise run storybook-start-test`; use the existing Storybook tools and browser
workflow below for component verification.

## Work as an agent

1. Run `mise run storybook-start`, then repeat `opencode mcp list` until
   `hearth-storybook` is connected.
   Start writes this checkout's endpoint to `.opencode/opencode.json`; do not
   register the server by hand. Discover its available tools rather than guessing
   their names or argument shapes.
2. Query its component index and component documentation before composing UI.
   Check the actual props and existing story examples. Read production source
   when a component is not documented yet.
3. Get the server's story-authoring instructions. Add or update colocated
   `*.stories.tsx` files with typed args and one clearly named use case per story.
4. Use the test tool for affected stories. Fix render, interaction, and
   accessibility failures rather than suppressing them.
5. Open direct story previews in a real browser using the printed `Local:` URL.
   Do not assume 6006; linked worktrees use a different port. Check desktop and
   phone widths, keyboard interaction, focus, overlays, and the relevant
   error/loading state. Automated axe checks do not establish complete
   accessibility.
6. Return direct story URLs and the checks actually run. Before delivery, run the
   full repository completion gate described in [testing](testing.md).

Stories tagged `ai-generated` await human review. Remove that tag after reviewing
their data, behavior, and appearance. `needs-work` marks stories not yet passing
browser verification and must not be mistaken for reviewed examples. The story
tests skip them.

## Add a story with realistic data

The shared `.storybook/preview.tsx` supplies production dark-only CSS,
`MemoryRouter`, and a fresh `ViewDataProvider` per story. Radix portals mount into
the preview document's body. Query portalled content through
`canvasElement.ownerDocument.body` in a play function.

The preview resets only Hearth's Core base URL and restores its previous value
after the story. MockDate fixes the wall clock without replacing animation,
interaction, or polling timers. Fixture timestamps, history ranges, and forecast
periods must agree with that clock.

Reuse the typed synthetic data and endpoint handlers in `.storybook/`. Fixtures
use existing API and catalog types rather than a second story-only contract.
Keep mutable Command state story-local and reset it between stories. A mock
Command response is not proof of a real device effect.

Override an endpoint in a story's `beforeEach({ msw })` using `msw.use(...)`.
Use explicit method/path handlers for loading, empty, unavailable, rejected, or
transport-error cases. An unhandled Core API request receives a 500 response and
fails its story. The preview's Vite configuration has no live backend proxies.
Keep assertions about observed State distinct from Command outcomes.

Use `Meta<typeof Component>` and `StoryObj<typeof meta>`, real component imports,
and object-literal args so the manifest can extract complete snippets. Set an
explicit `title` for the catalog category, independent of the source folder:
`Components/<Component>` for shared UI and compositions,
`Components/Cards/<Component>` for cards, `Components/Controls/<Component>` for
feature controls, `Components/Editors/<Component>` for editors, `Pages/<Page>`
for routed screens, and `Application/<Journey>` for application scenarios. Keep
the story colocated with its source owner. Prefer
direct component stories for discoverable props, with
renderer/composition stories for catalog layouts. Add a play function when it
proves async data arrival, a meaningful interaction, or a portal contract. A
story that reads Entity data needs one awaited assertion on the content that
defines its state. Without it, the accessibility check audits the loading state
and the story passes whatever it renders. A story that renders synchronously
from args needs no play assertion. A phone story also calls
`expectNoHorizontalOverflow` from `.storybook/play.ts`. In a card interaction
story, start from the ready card and wrap the interaction and its assertions in
`watchHeight` from the same file. Include narrow widths and pending or failed
feedback where relevant. Keep the single `CssCheck` story as the production
CSS loading check.

Query controls by their accessible role and name, and fields by their label.
Use `data-slot` when a test must identify a structural part that has no distinct
accessible role, such as a card body or reading value. Slots are component
contracts; do not use styling classes as test selectors. For example, use
`canvas.getByRole("button", { name: "Source" })` for the Source action and
`canvasElement.querySelector('[data-slot="card-body"]')` for the frame body.

## Show motion in a story

When a component adopts a motion pattern from DESIGN.md (Motion), add a story
that triggers it. Name it `Motion · <what moves>`, such as `Motion · Afterglow`,
and add the `motion` tag so the sidebar's tag filter finds it. Its play
function triggers the change and asserts the settled result; a card story wraps
the interaction in `watchHeight`. Add a row for the story to the Where it moves
table on Design system/Motion (`web/src/design-system/motion.mdx`).

Story tests run without a reduced-motion preference. Check the reduced path in
a browser with reduced motion emulated, and report it as unverified otherwise.

## Choose a component, editor, or page story

Use the live sidebar or MCP index as the component catalog, not a copied list.
Choose a direct component story for props, `Pages` for isolated screen reads,
or `Application` for navigation and provider lifecycles. Stories and tests stay
with their source owner; shared components do not import features.

Write an `Application` story only when its assertions span what the assembled
application connects: routes, the workspace provider, a conversation session
sending, queueing, or refreshing history over HTTP, the unsaved-changes guard,
or shell layout across widths. If one component with
the preview's router and the fixture handlers reproduces every assertion, write
a component or page story instead. A component that reads a provider, such as
`ConversationHistory`, wraps it in a story decorator.

For a conversation thread state, write a `Components/ConversationThread`
story over the fixture conversation in
[`.storybook/fixture-conversation.ts`](../../web/.storybook/fixture-conversation.ts).
It holds the session snapshot that `ConversationThread` renders and
changes it synchronously: `send` shows a prompt and starts a live turn, `apply`
reduces `TurnEvent`s with the production `reduceTurn`, and `commit` stores
history the way the post-turn read does. Seed it with `parameters.conversation`,
using `conversationHistory` for stored rows. In a play function, send through
the composer, then apply the events from `fixtureTurn` in
[application fixtures](../../web/.storybook/application-fixtures.ts), which the
synthetic stream also sends. Core spaces a view's events out; wait for each
placeholder to appear before applying the next batch. The fixture proves what
the thread renders for a session state, not how `ConversationSession` reaches
that state; sending and queueing stay `Application` stories.

Read the exported story before relying on its coverage. Editor fixtures hold
local drafts: changing Controls resets them, while canvas edits do not update
Controls. A mounted editor or visible control is not proof that its interaction
is tested. Page-read fixtures do not mock Rename or enablement PATCH requests;
those actions fail as unhandled requests. Direct control stories can update
synthetic observed State, but do not establish physical effects.

Application scenarios live in [`src/app-stories/`](../../web/src/app-stories),
one file per journey. Each file builds its meta from the members of
`applicationMeta` in [the application harness](../../web/.storybook/application.tsx)
and names them individually. Keep `title`, `component`, `tags`, and `render`
literal in the file: Storybook's indexer and the manifest's snippet extractor
read them statically and reject a spread of the shared object. Shared play
helpers, such as `askTemperature` and `editTitle`, live in the same harness.
Fixture data lives in [application fixtures](../../web/.storybook/application-fixtures.ts).
`applicationMeta` sets `parameters: { application: true }` to let the
application supply its shell, providers, and isolated router instead of the
shared wrapper. `appPath` selects the initial route; `applicationScenario`
selects the fixture. Reset mutable
history and revisions between stories, and cancel open streams on unmount.
Keep application stories fullscreen without inline Autodocs so live applications
do not share mocked endpoints. Sending, saving, and Automation editing affect
synthetic data only; these scenarios neither execute Automations nor prove
backend persistence or model behavior.

Generated App snippets omit scenario args and play functions. They are not
standalone reproductions. Use the exported story and shared fixture setup;
manifest checks verify extraction, not omitted scenario inputs.

For a page that reads route params, set a `route` parameter on its meta:

```tsx
parameters: {
  route: {
    path: "/entities/:entityId",
    entry: `/entities/${entityId(4)}`,
  },
}
```

The preview mounts a fresh MemoryRouter at `entry` and matches `path` with a
Route. Without `route`, stories keep the bare router. Do not add a second router
inside a story.

Prop-less pages have explicit exceptions in
[the manifest checker](../../web/scripts/check-storybook-manifest.ts), which still
checks their snippets. Every reusable component must publish nonempty prop docs.
Native and Radix prop filtering in `.storybook/main.ts` must retain the props a
new story demonstrates.

The generated snippets currently use a `hearth-dashboard` package import, but this
application does not publish component exports. Use the source-module import from
the colocated story instead. Storybook's **Start here** page records this caveat
for MCP clients too. `.pnpmfile.cjs` gives the legacy Storybook/docgen stack
TypeScript 6's JavaScript compiler API; application typechecking stays on
TypeScript 7. Remove that compatibility hook when upstream supports TypeScript 7.

## Run checks and build

```sh
mise run --skip-deps web-test
mise run --skip-deps web-build
mise run --skip-deps storybook-test
mise run --skip-deps storybook-test -- toggle-tile.stories.tsx
mise run --skip-deps storybook-build
```

Existing frontend tests run in the `unit` Vitest project with jsdom. Story tests
run in Playwright Chromium, including play functions and axe checks, in two
projects. `storybook` runs component and page story files in parallel browser
workers. `storybook-application` runs the files in `src/app-stories/` one at a
time, because each runs the whole application with streamed replies and
animated layout. Under that load, `.storybook/preview.tsx` raises the `findBy*`
and `waitFor` limit to 3 seconds; a fixture that streams on a timer still needs
to finish well inside it. Accessibility violations fail by default. Fix in-scope
defects; discuss unrelated accessibility debt rather than disabling rules or
changing the global failure policy.

CI installs Chromium with system dependencies, tests the stories, and builds an
artifact named `storybook` retained for 14 days. Download it to inspect the static
build locally; it is not a hosted preview and has no MCP server. No paid visual
regression service is configured.

The Storybook build also checks the generated agent manifest for missing props,
docgen errors, empty snippets, and extraction errors. A rendered story can pass while its agent
documentation is broken, so both checks are required.

If Chromium cannot launch on Linux, inspect the reported missing libraries.
On supported Debian/Ubuntu systems, use:

```sh
mise run storybook-browser-install -- --with-deps
```

Alternatively, install system `chromium` (or `chromium-browser`) on `PATH`.
Both launch paths select it automatically, without OS-specific configuration.

Report unavailable browser checks as unverified. Do not substitute jsdom results
for browser evidence.
