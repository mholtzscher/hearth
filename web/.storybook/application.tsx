import type { Meta, StoryObj } from "@storybook/react-vite";
import { StrictMode } from "react";
import type { ComponentProps } from "react";
import { createMemoryRouter } from "react-router-dom";
import { expect, waitFor, within } from "storybook/test";

import type App from "../src/app";
import { appRoutes } from "../src/app";
import {
  conversationPath,
  createApplicationHandlers,
} from "./application-fixtures";

/**
 * The shared setup for `Application/*` stories. Each story file names these
 * members and keeps `title`, `component`, `tags`, and `render` literal: the
 * indexer and the manifest's snippet extractor read those statically and cannot
 * resolve a spread of this object.
 */
export const applicationMeta = {
  decorators: [
    (Story) => (
      <StrictMode>
        <Story />
      </StrictMode>
    ),
  ],
  parameters: {
    application: true,
    layout: "fullscreen",
    appPath: "/",
    docs: {
      // A live router contains circular state; use the extracted source instead of serializing its props.
      source: { type: "code" },
      description: {
        component:
          "The production application with an isolated memory router and synthetic Core HTTP/SSE. These fullscreen stories include real navigation, conversation, and canvas providers. They do not contact a model or physical Devices.",
      },
    },
  },
  argTypes: { router: { control: false } },
  loaders: [
    ({ parameters }) => ({
      router: createMemoryRouter(appRoutes, {
        initialEntries: [parameters.appPath],
      }),
    }),
  ],
  beforeEach({ msw, parameters, loaded }) {
    msw.use(...createApplicationHandlers(parameters.applicationScenario));
    return () => loaded.router.dispose();
  },
} satisfies Omit<Meta<typeof App>, "title" | "component" | "tags" | "render">;

// The loader supplies the required router; it is not a serializable Control.
export type ApplicationStory = StoryObj<Partial<ComponentProps<typeof App>>>;
export type ApplicationContext = Parameters<
  NonNullable<ApplicationStory["play"]>
>[0];

export const longSelectedTitle =
  "Detailed whole-home climate, lighting and comfort overview for every room";

export const askTemperature = async ({
  canvas,
  userEvent,
}: ApplicationContext) => {
  const prompt = await canvas.findByRole("textbox", { name: "Ask Hearth" });
  await waitFor(() => expect(prompt).toBeEnabled());
  await userEvent.click(prompt);
  await userEvent.paste("What is the living room temperature?");
  await userEvent.click(canvas.getByRole("button", { name: "Send message" }));
};

/** Start DOM assertions only after the mock returns post-turn history. */
export const sendAndWaitForHistory = async (
  { msw }: ApplicationContext,
  send: () => Promise<void>
) => {
  const history = Promise.withResolvers<Response>();
  const listener = new AbortController();
  msw.events.on(
    "response:mocked",
    ({ request, response }: { request: Request; response: Response }) => {
      if (
        request.method === "GET" &&
        new URL(request.url).pathname ===
          `/v1/agent${conversationPath}/messages`
      ) {
        history.resolve(response);
      }
    },
    { signal: listener.signal }
  );
  try {
    await send();
    AbortSignal.timeout(5000).addEventListener(
      "abort",
      () =>
        history.reject(new Error("No refreshed conversation history response")),
      { once: true, signal: listener.signal }
    );
    const response = await history.promise;
    await expect(response.ok).toBe(true);
  } finally {
    listener.abort();
  }
};

export const editTitle = async (
  { canvas, userEvent }: ApplicationContext,
  title: string
) => {
  const view = within(
    await canvas.findByRole("region", { name: "View canvas" })
  );
  await view.findByRole("heading", { name: "Morning overview" });
  await userEvent.click(view.getByRole("button", { name: "Edit" }));
  const input = view.getByRole("textbox", { name: "Dashboard title" });
  await userEvent.clear(input);
  if (title) {
    await userEvent.paste(title);
  }
  return view;
};

export const expectSelectedContextFits = async ({
  canvas,
}: ApplicationContext) => {
  const selected = within(canvas.getByRole("group", { name: "Selected view" }));
  const show = selected.getByRole("button", {
    name: `Show selected view: ${longSelectedTitle}`,
  });
  const close = selected.getByRole("button", { name: "Close selected view" });
  const send = canvas.getByRole("button", { name: "Send message" });
  const group = canvas
    .getByRole("group", { name: "Selected view" })
    .getBoundingClientRect();
  const showRect = show.getBoundingClientRect();
  const closeRect = close.getBoundingClientRect();
  const sendRect = send.getBoundingClientRect();
  await expect(closeRect.right).toBeLessThanOrEqual(group.right + 0.5);
  await expect(group.right).toBeLessThanOrEqual(sendRect.left + 0.5);
  await expect(showRect.right).toBeLessThanOrEqual(closeRect.left + 0.5);
  await expect(closeRect.width).toBe(44);
  await expect(closeRect.height).toBe(44);
  await expect(showRect.width).toBeGreaterThanOrEqual(44);
  await expect(showRect.height).toBe(44);
  await expect(sendRect.width).toBeGreaterThanOrEqual(44);
  await expect(sendRect.height).toBe(44);
  const title = within(show).getByText(longSelectedTitle);
  await expect(show).toHaveAttribute("title", longSelectedTitle);
  await expect(title.clientWidth).toBeGreaterThan(0);
  await expect(title.scrollWidth).toBeGreaterThan(title.clientWidth);
  await expect(getComputedStyle(title).textOverflow).toBe("ellipsis");
};

/** Names of the CSS animations on an element and its descendants. */
export const animationNames = (element: Element | null | undefined): string[] =>
  element
    ?.getAnimations({ subtree: true })
    .map((animation) =>
      animation instanceof CSSAnimation ? animation.animationName : ""
    ) ?? [];
