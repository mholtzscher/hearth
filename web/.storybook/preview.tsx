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
      onUnhandledRequest(request, print) {
        const path = new URL(request.url).pathname;
        if (/^\/(v1(?:\/|$)|healthz$|readyz$|nats-monitor(?:\/|$))/.test(path)) print.error();
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
    return () => {
      MockDate.reset();
      setBaseUrl(previousBase ?? "");
      if (previousBase === null) localStorage.removeItem("hearth.baseUrl");
    };
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
