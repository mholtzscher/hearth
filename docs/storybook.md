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
browser verification and must not be mistaken for reviewed examples.

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
transport-error cases. The preview blocks unhandled Core API requests, and its
Vite configuration has no live backend proxies. Keep assertions about observed
State distinct from Command outcomes.

Use `Meta<typeof Component>` and `StoryObj<typeof meta>`, real component imports,
and object-literal args so the manifest can extract complete snippets. Card
stories use the title `features/views/cards/<Component>`. Storybook omits a
folder from the generated title only when the folder name matches the file name,
so set `title` explicitly for a card in a kebab-case folder such as
`toggle-tile/ToggleTile.stories.tsx`. Prefer
direct component stories for discoverable props, with
renderer/composition stories for catalog layouts. Add a play function when it
proves async data arrival, a meaningful interaction, or a portal contract. Static
variants need no repetitive play assertion. Keep the single `CssCheck` story as
the production CSS loading check.

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
docgen errors, and incomplete snippets. A rendered story can pass while its agent
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
