import { DocsContainer } from "@storybook/addon-docs/blocks";
import type { Preview } from "@storybook/react-vite";
import MockDate from "mockdate";
import { mswLoader } from "msw-storybook-addon/csf3";
import { setupWorker } from "msw/browser";
import type { ComponentProps } from "react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { create } from "storybook/theming";
import type { ThemeVars } from "storybook/theming";

import { setBaseUrl } from "../src/api/client";
import { ViewDataProvider } from "../src/features/views/data/provider";
import { cn } from "../src/lib/utils";

import "../src/index.css";
import { fixtureNow, createCoreHandlers } from "./msw-handlers";

/**
 * Docs pages use Hearth's dark tokens. Storybook's theme math needs plain colors,
 * so read them from the live CSS when a docs page first renders.
 */
let docsTheme: ThemeVars | undefined;
const hearthDocsTheme = (): ThemeVars => {
  if (!docsTheme) {
    const root = getComputedStyle(document.documentElement);
    const token = (name: string) => root.getPropertyValue(`--${name}`).trim();
    docsTheme = create({
      base: "dark",
      appBg: token("gray-1"),
      appContentBg: token("background"),
      appPreviewBg: token("background"),
      appBorderColor: token("border"),
      barBg: token("gray-1"),
      colorSecondary: token("violet"),
      fontBase: '"Figtree Variable", sans-serif',
      textColor: token("foreground"),
      textMutedColor: token("muted-foreground"),
    });
  }
  return docsTheme;
};
const HearthDocsContainer = (props: ComponentProps<typeof DocsContainer>) => (
  <DocsContainer {...props} theme={hearthDocsTheme()} />
);

// Core requests that no handler answered during the current story.
const unhandledCoreRequests: string[] = [];

const preview: Preview = {
  tags: ["autodocs"],
  decorators: [
    (Story, context) =>
      context.parameters.application ? (
        <Story />
      ) : (
        <MemoryRouter
          key={context.id}
          initialEntries={[context.parameters.route?.entry ?? "/"]}
        >
          <ViewDataProvider key={context.id}>
            <main
              className={cn(
                "bg-background text-label text-foreground max-tablet:p-3 p-6 *:mx-auto *:max-w-[1080px]",
                // An inline docs story takes its content's height, not the viewport's.
                context.viewMode !== "docs" && "min-h-screen"
              )}
            >
              {context.parameters.route ? (
                <Routes>
                  <Route
                    path={context.parameters.route.path}
                    element={<Story />}
                  />
                </Routes>
              ) : (
                <Story />
              )}
            </main>
          </ViewDataProvider>
        </MemoryRouter>
      ),
  ],
  loaders: [
    mswLoader(async () => {
      const worker = setupWorker();
      await worker.start({
        quiet: true,
        // Throwing answers the request with a 500. The default handles only print and pass it through.
        async onUnhandledFrame({ frame, defaults }) {
          if (!("request" in frame.data)) {
            return;
          }
          const { method, url } = frame.data.request;
          const path = new URL(url).pathname;
          if (
            !/^\/(?:v1(?:\/|$)|healthz$|readyz$|nats-monitor(?:\/|$))/u.test(
              path
            )
          ) {
            return;
          }
          unhandledCoreRequests.push(`${method} ${path}`);
          await defaults.error();
          throw new Error(`Unhandled Core request: ${method} ${path}`);
        },
      });
      return worker;
    }),
  ],
  beforeEach(context) {
    const { msw } = context;
    const previousBase = localStorage.getItem("hearth.baseUrl");
    setBaseUrl("");
    MockDate.set(fixtureNow);
    msw.use(...createCoreHandlers());
    unhandledCoreRequests.length = 0;
    return () => {
      MockDate.reset();
      setBaseUrl(previousBase ?? "");
      if (previousBase === null) {
        localStorage.removeItem("hearth.baseUrl");
      }
    };
  },
  // A component can absorb the failed request, so fail the story as well.
  afterEach() {
    if (unhandledCoreRequests.length > 0) {
      throw new Error(
        `Unhandled Core requests: ${unhandledCoreRequests.join(", ")}`
      );
    }
  },
  parameters: {
    options: {
      storySort: {
        method: "alphabetical",
        order: [
          "Start here",
          "Design system",
          [
            "Overview",
            "Foundations",
            "Components",
            "View cards",
            "Do's and Don'ts",
          ],
          "Components",
          "Pages",
          "Application",
        ],
      },
    },
    docs: { container: HearthDocsContainer },
    layout: "fullscreen",
    controls: {
      matchers: {
        color: /(?:background|color)$/iu,
        date: /Date$/iu,
      },
    },
    a11y: { test: "error" },
    viewport: {
      options: {
        phone: { name: "Phone", styles: { width: "390px", height: "844px" } },
        tablet: { name: "Tablet", styles: { width: "900px", height: "900px" } },
        desktop: {
          name: "Desktop",
          styles: { width: "1440px", height: "1000px" },
        },
      },
    },
  },
};

export default preview;
