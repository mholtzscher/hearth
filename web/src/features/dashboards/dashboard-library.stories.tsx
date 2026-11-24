import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import {
  createApplicationHandlers,
  dashboardPath,
} from "../../../.storybook/application-fixtures";
import { expectNoHorizontalOverflow } from "../../../.storybook/play";
import { DashboardLibrary } from "./dashboard-library";

const meta = {
  title: "Pages/DashboardLibrary",
  component: DashboardLibrary,
  tags: ["ai-generated"],
  parameters: {
    route: { path: "/dashboards", entry: "/dashboards" },
    docs: {
      description: {
        component:
          "Saved dashboards with open and delete actions. It takes no props. Deletion changes only the story's synthetic dashboard list.",
      },
    },
  },
  beforeEach({ msw, parameters }) {
    msw.use(...createApplicationHandlers(parameters.applicationScenario));
  },
} satisfies Meta<typeof DashboardLibrary>;
export default meta;
type Story = StoryObj<typeof meta>;

export const Loaded: Story = {
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("heading", { name: "Morning overview" })
    ).toBeVisible();
    await expect(
      canvas.getByRole("link", { name: "Morning overview" })
    ).toHaveAttribute("href", dashboardPath);
    const remove = canvas.getByRole("button", {
      name: "Delete Morning overview",
    });
    await expect(remove.getBoundingClientRect().height).toBe(44);
  },
};

export const Loading: Story = {
  beforeEach({ msw }) {
    msw.use(
      http.get("/v1/dashboards", async ({ request }) => {
        const aborted = Promise.withResolvers<undefined>();
        request.signal.addEventListener(
          "abort",
          () => aborted.resolve(undefined),
          { once: true }
        );
        await aborted.promise;
        return HttpResponse.error();
      })
    );
  },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByText("Loading dashboards…")
    ).toBeInTheDocument();
    await expect(
      canvas.queryByRole("heading", {
        name: "Your first dashboard starts with a question.",
      })
    ).not.toBeInTheDocument();
  },
};

export const LoadFailure: Story = {
  beforeEach({ msw }) {
    msw.use(
      http.get("/v1/dashboards", () =>
        HttpResponse.json({ detail: "Core is unavailable" }, { status: 503 })
      )
    );
  },
  play: async ({ canvas }) => {
    const alert = within(await canvas.findByRole("alert"));
    await expect(alert.getByRole("button", { name: "Reload" })).toBeVisible();
    await expect(
      canvas.queryByRole("heading", {
        name: "Your first dashboard starts with a question.",
      })
    ).not.toBeInTheDocument();
  },
};

export const Empty: Story = {
  parameters: { applicationScenario: { dashboards: "empty" } },
  play: async ({ canvas }) => {
    await expect(
      await canvas.findByRole("heading", {
        name: "Your first dashboard starts with a question.",
      })
    ).toBeVisible();
    await expect(
      canvas.getByRole("link", { name: "Start a conversation →" })
    ).toHaveAttribute("href", "/");
  },
};

export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async (context) => {
    const { play } = Loaded;
    if (!play) {
      throw new Error("Expected the Loaded play function");
    }
    await play(context);
    await expectNoHorizontalOverflow(context.canvasElement);
  },
};

export const Delete: Story = {
  play: async ({ canvas, canvasElement, userEvent }) => {
    const remove = await canvas.findByRole("button", {
      name: "Delete Morning overview",
    });
    await userEvent.click(remove);
    const body = within(canvasElement.ownerDocument.body);
    const dialog = within(
      body.getByRole("dialog", { name: "Remove this dashboard?" })
    );
    await expect(
      dialog.getByText(/Your home data and conversation remain unchanged/u)
    ).toBeVisible();
    await userEvent.click(dialog.getByRole("button", { name: "Cancel" }));
    await expect(remove).toHaveFocus();
    await userEvent.click(remove);
    await userEvent.click(
      body.getByRole("button", { name: "Delete dashboard" })
    );
    await expect(
      await canvas.findByRole("heading", {
        name: "Your first dashboard starts with a question.",
      })
    ).toBeVisible();
  },
};

export const PhoneDeleteConfirmation: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(
      await canvas.findByRole("button", { name: "Delete Morning overview" })
    );
    const dialog = within(
      within(canvasElement.ownerDocument.body).getByRole("dialog", {
        name: "Remove this dashboard?",
      })
    );
    await expect(dialog.getByRole("button", { name: "Cancel" })).toHaveFocus();
    await expect(
      dialog.getByText(/Your home data and conversation remain unchanged/u)
    ).toBeVisible();
    await expectNoHorizontalOverflow(canvasElement);
  },
};
