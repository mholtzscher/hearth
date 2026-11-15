# Develop the dashboard with Storybook

Storybook runs the production React components with Hearth's dark theme and
synthetic Core HTTP responses. Core, NATS, model credentials, and physical devices
are not needed. These stories verify browser component behavior, not backend
semantics or device compatibility.

## Start the development loop

From the repository root:

```sh
mise run web-install
mise run storybook-browser-install
mise run storybook
```

Open <http://127.0.0.1:6006>. Stop the foreground server with Ctrl+C. The server
binds loopback, does not open a browser automatically, and fails if port 6006 is
occupied instead of silently selecting another project's MCP endpoint.

For parallel worktrees, choose a free port:

```sh
mise run storybook -- --port 6016
opencode mcp add hearth-storybook --url http://127.0.0.1:6016/mcp
```

Keep the project-local MCP URL aligned with that worktree's running server.
OpenCode watches configuration changes. Its checked-in default is
<http://127.0.0.1:6006/mcp>; `opencode mcp list` verifies the connection. Other MCP
clients can connect to the same Streamable HTTP endpoint without credentials.
Storybook's MCP and component manifest features are upstream preview features.

## Work as an agent

1. Start Storybook and confirm `hearth-storybook` is connected. Discover its
   available tools rather than guessing their names or argument shapes.
2. Query its component index and component documentation before composing UI.
   Check the actual props and existing story examples. Read production source
   when a component is not documented yet.
3. Get the server's story-authoring instructions. Add or update colocated
   `*.stories.tsx` files with typed args and one clearly named use case per story.
4. Use the test tool for affected stories. Fix render, interaction, and
   accessibility failures rather than suppressing them.
5. Open direct story previews in a real browser. Check desktop and phone widths,
   keyboard interaction, focus, overlays, and the relevant error/loading state.
   Automated axe checks do not establish complete accessibility.
6. Return direct story URLs and the checks actually run. Before delivery, run the
   full repository completion gate described in [testing](testing.md).

Stories tagged `ai-generated` await human review. Remove that tag after reviewing
their data, behavior, and appearance. `needs-work` marks stories not yet passing
browser verification and must not be mistaken for reviewed examples. The story
tests skip them.

## Add a story with realistic data

The shared `.storybook/preview.tsx` supplies production CSS, the `dark` class,
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
and object-literal args so the manifest can extract complete snippets. Card
stories use the title `features/views/cards/<Component>`. Storybook omits a
folder from the generated title only when the folder name matches the file name,
so set `title` explicitly for a card in a kebab-case folder such as
`toggle-tile/ToggleTile.stories.tsx`. Prefer
direct component stories for discoverable props, with
renderer/composition stories for catalog layouts. Add a play function when it
proves async data arrival, a meaningful interaction, or a portal contract. A
story that reads Entity data needs one awaited assertion on the content that
defines its state. Without it, the accessibility check audits the loading state
and the story passes whatever it renders. A story that renders synchronously
from args needs no play assertion. A phone story also calls
`expectNoHorizontalOverflow` from `.storybook/play.ts`. Keep the single
`CssCheck` story as the production CSS loading check.

Query controls by their accessible role and name, and fields by their label.
Use `data-slot` when a test must identify a structural part that has no distinct
accessible role, such as a card body or reading value. Slots are component
contracts; do not use styling classes as test selectors. For example, use
`canvas.getByRole("button", { name: "Source" })` for the Source action and
`canvasElement.querySelector('[data-slot="card-body"]')` for the frame body.

## Choose a component, editor, or page story

The sidebar includes the catalog's bound cards, their shared frame and Source
details, light and power controls, and reusable UI components. Direct editor
stories cover Metric, ToggleTile, AirPurifier, and TimeSeries. Each holds a local
draft so you can make several edits in the canvas. Changing the initial value in
Controls resets that draft; canvas edits do not update Controls. Other card
editors have no direct stories. ViewEditor mounts Device and OutsideNow editors
inside collapsed sections, but its stories do not interact with those sections.

The `components` catalog includes colocated stories for Button, Input, Textarea,
NativeSelect, Switch, SegmentedControl, Dialog, Popover, Notice, Accordion,
Badge, Card, ChartContainer, Label, Separator, and Table. ChartContainer also
appears in TimeSeries stories.

Shared controls and compositions live in `src/components` and do not import
features. Feature-specific stories and tests live alongside their owning feature.
Shared status and error UI may use common Hearth vocabulary without becoming
feature-owned.

The `application/Workspace` group runs the real application shell, conversation,
and full dashboard canvas together. It includes desktop, tablet, and phone
navigation, readiness states, conversation history and tool activity, synthetic
streamed replies, send failure/retry, and dashboard editing, save conflicts,
discard confirmation, expansion, and phone panel switching. It also covers the
dashboard library (loaded, empty, deletion, and phone layout), conversation search,
pending views, selected-view messages, invalid drafts, and reading older messages.
Its Commands story verifies status filtering and clearing the filter on the
Development page. Automations have their own application group.

The `application/Automations` group covers the Automation library and new-draft
canvas, including a 390px phone layout. Direct AutomationCard and AutomationEditor
stories cover prose, step stacks, exact definitions, referenced-child inspection,
editing, and invalid input. These fixtures do not execute Automations.

Application stories set `parameters: { application: true }` to bypass the shared
component router and theme wrapper. The application supplies its own production
shell and providers. Each story owns an isolated memory router over `appRoutes`
and creates fresh HTTP/SSE handlers from `.storybook/application-fixtures.ts`.
Mutable history and saved revisions reset between stories. The pending-reply
story deliberately leaves its stream open; the conversation provider cancels
the request when the story unmounts.

Use `appPath` to select the initial route and `applicationScenario` for the named
server, turn, history-failure, and save-conflict fixtures. Application stories
are fullscreen and omit inline Autodocs, so multiple live applications do not
share a Docs page's mocked endpoints. Sending and saving affect synthetic data
only. These scenarios do not establish model behavior, backend persistence, or
Device effects.

The generated App snippets show the shared render function, not each story's
`appPath`, `applicationScenario`, or play function. They are not standalone
examples. To reproduce one of these composition scenarios, use its exported
story and the shared setup in `src/App.stories.tsx`, together with
`.storybook/application-fixtures.ts`. The manifest check verifies snippet
presence and extraction errors; it cannot verify those omitted scenario inputs.

The `pages` group runs EntitiesPage, DevicesPage, and EntityDetailPage against the
same synthetic household. Use its loaded, empty, and failed-read stories to
inspect the page without Core. The Entity detail's `NeverObserved` story also
empties retained history. Device selection and Entity filtering have interaction
tests; this is not full application navigation or a backend E2E test.
These page-read scenarios do not mock Rename or enablement PATCH requests. Using
those controls produces an unhandled-request error rather than changing Core.
Direct control stories do refresh their synthetic observed State after Commands.

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

These three pages take no props, so docgen emits no API metadata for them. The
manifest check allows that specific diagnostic for their three IDs while still
checking snippets. Every reusable component must publish nonempty prop docs.
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
mise run --skip-deps storybook-test -- ToggleTile.stories.tsx
mise run --skip-deps storybook-build
```

Existing frontend tests run in the `unit` Vitest project with jsdom. Story tests
run in the separate `storybook` project with Playwright Chromium, including play
functions and axe checks. Accessibility violations fail by default. Fix in-scope
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

On NixOS, use the system-packaged Chromium instead of Playwright's downloaded
Linux binary. Set this in the shell that starts Storybook or runs its tests:

```sh
export PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH="$(command -v chromium)"
```

Report unavailable browser checks as unverified. Do not substitute jsdom results
for browser evidence.
