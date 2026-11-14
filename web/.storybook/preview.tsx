import type { Preview } from "@storybook/react-vite";
import MockDate from "mockdate";
import { MemoryRouter } from "react-router-dom";
import { mswLoader } from "msw-storybook-addon/csf3";
import { setupWorker } from "msw/browser";
import { setBaseUrl } from "../src/api/client";
import { ViewDataProvider } from "../src/features/views/data/provider";
import "../src/index.css";
import "./preview.css";
import { fixtureNow, createCoreHandlers } from "./msw-handlers";

// Core requests that no handler answered during the current story.
const unhandledCoreRequests: string[] = [];

const preview: Preview = {
  tags: ["autodocs"],
  decorators: [
    (Story, context) => (
      <MemoryRouter>
        <ViewDataProvider key={context.id}>
          <main className="hearth-app hearth-story">
            <Story />
          </main>
        </ViewDataProvider>
      </MemoryRouter>
    ),
  ],
  loaders: [mswLoader(async () => {
    const worker = setupWorker();
    await worker.start({
      quiet: true,
      // Throwing answers the request with a 500. The default handles only print and pass it through.
      async onUnhandledFrame({ frame, defaults }) {
        if (!("request" in frame.data)) return;
        const { method, url } = frame.data.request;
        const path = new URL(url).pathname;
        if (!/^\/(v1(?:\/|$)|healthz$|readyz$|nats-monitor(?:\/|$))/.test(path)) return;
        unhandledCoreRequests.push(`${method} ${path}`);
        await defaults.error();
        throw new Error(`Unhandled Core request: ${method} ${path}`);
      },
    });
    return worker;
  })],
  beforeEach({ msw }) {
    document.documentElement.classList.add("dark");
    const previousBase = localStorage.getItem("hearth.baseUrl");
    setBaseUrl("");
    MockDate.set(fixtureNow);
    msw.use(...createCoreHandlers());
    unhandledCoreRequests.length = 0;
    return () => {
      MockDate.reset();
      setBaseUrl(previousBase ?? "");
      if (previousBase === null) localStorage.removeItem("hearth.baseUrl");
    };
  },
  // A component can absorb the failed request, so fail the story as well.
  afterEach() {
    if (unhandledCoreRequests.length > 0) throw new Error(`Unhandled Core requests: ${unhandledCoreRequests.join(", ")}`);
  },
  parameters: {
    layout: "fullscreen",
    controls: {
      matchers: {
        color: /(background|color)$/i,
        date: /Date$/i,
      },
    },
    a11y: { test: "error" },
    viewport: {
      options: {
        phone: { name: "Phone", styles: { width: "390px", height: "844px" } },
        desktop: { name: "Desktop", styles: { width: "1440px", height: "1000px" } },
      },
    },
  },
};

export default preview;
