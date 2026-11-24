import type { Meta, StoryObj } from "@storybook/react-vite";
import { createRef } from "react";
import { expect, fn } from "storybook/test";

import { dashboardDefinition } from "../../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../../.storybook/play";
import { readDefinition } from "../views/catalog/compatibility";
import { CanvasHeader } from "./canvas-header";
import type { Draft } from "./draft";

const generated: Draft = {
  definition: dashboardDefinition,
  original: dashboardDefinition,
  base: null,
  origin_artifact_id: "artifact_story_morning",
  dirty: false,
};
const saved: Draft = {
  ...generated,
  base: { dashboard_id: "dashboard_story_morning", revision: 3 },
  origin_artifact_id: null,
};
const edited: Draft = { ...saved, dirty: true };

const meta = {
  title: "Components/CanvasHeader",
  component: CanvasHeader,
  tags: ["ai-generated"],
  // `Canvas` derives `blocked` and `unsaved` from the draft, saving, and editor validity; each story keeps them consistent.
  args: {
    view: readDefinition(dashboardDefinition),
    draft: saved,
    editor: false,
    expanded: false,
    expandButton: createRef<HTMLButtonElement>(),
    saving: false,
    blocked: false,
    unsaved: false,
    editorValid: true,
    onToggleEditor: fn(),
    onCustomize: fn(),
    onDiscard: fn(),
    onSave: fn(),
    onSaveNew: fn(),
    onToggleExpanded: fn(),
    onClose: fn(),
  },
  argTypes: {
    draft: { control: false },
    view: { control: false },
    expandButton: { control: false },
  },
  parameters: {
    docs: {
      description: {
        component:
          "The view canvas header: title and save state, view actions, and window controls. `Canvas` owns the workspace draft and passes the derived `blocked` and `unsaved` states.",
      },
    },
  },
} satisfies Meta<typeof CanvasHeader>;
export default meta;
type Story = StoryObj<typeof meta>;

/** A view from a reply is unsaved until it becomes a dashboard. */
export const GeneratedView: Story = {
  args: { draft: generated, unsaved: true },
  play: async ({ args, canvas, userEvent }) => {
    await expect(canvas.getByText(/Generated view/u)).toHaveTextContent(
      "Generated view · Saved"
    );
    await expect(
      canvas.queryByRole("button", { name: "Save as new" })
    ).not.toBeInTheDocument();
    await userEvent.click(
      canvas.getByRole("button", { name: "Save dashboard" })
    );
    await expect(args.onSave).toHaveBeenCalledOnce();
    await userEvent.click(
      canvas.getByRole("button", { name: "Ask Hearth to change" })
    );
    await expect(args.onCustomize).toHaveBeenCalledOnce();
    await userEvent.click(canvas.getByRole("button", { name: "Close view" }));
    await expect(args.onClose).toHaveBeenCalledOnce();
  },
};

export const SavedDashboard: Story = {
  play: async ({ args, canvas, userEvent }) => {
    await expect(
      canvas.getByRole("heading", { name: "Morning overview" })
    ).toBeVisible();
    await expect(canvas.getByText(/revision 3/u)).toHaveTextContent(
      "Dashboard · revision 3 · Saved"
    );
    await expect(
      canvas.getByRole("button", { name: "Save changes" })
    ).toBeDisabled();
    await expect(
      canvas.queryByRole("button", { name: "Discard changes" })
    ).not.toBeInTheDocument();
    await userEvent.click(canvas.getByRole("button", { name: "Edit" }));
    await expect(args.onToggleEditor).toHaveBeenCalledOnce();
    await userEvent.click(canvas.getByRole("button", { name: "Expand" }));
    await expect(args.onToggleExpanded).toHaveBeenCalledOnce();
  },
};

export const UnsavedChanges: Story = {
  args: { draft: edited, unsaved: true },
  play: async ({ args, canvas, userEvent }) => {
    await expect(canvas.getByText("· Unsaved changes")).toBeVisible();
    await userEvent.click(canvas.getByRole("button", { name: "Save changes" }));
    await expect(args.onSave).toHaveBeenCalledOnce();
    await userEvent.click(canvas.getByRole("button", { name: "Save as new" }));
    await expect(args.onSaveNew).toHaveBeenCalledOnce();
    await userEvent.click(
      canvas.getByRole("button", { name: "Discard changes" })
    );
    await expect(args.onDiscard).toHaveBeenCalledOnce();
  },
};

/** While a save runs, every action that would change or leave the draft waits. */
export const Saving: Story = {
  args: { draft: edited, unsaved: true, saving: true, blocked: true },
  play: async ({ canvas }) => {
    await Promise.all(
      [
        "Edit",
        "Ask Hearth to change",
        "Discard changes",
        "Save as new",
        "Save changes",
        "Close view",
      ].map((name) =>
        expect(canvas.getByRole("button", { name })).toBeDisabled()
      )
    );
  },
};

/** Invalid editor input blocks saving and asking Hearth, but can still be discarded. */
export const InvalidEditor: Story = {
  args: {
    draft: edited,
    unsaved: true,
    editor: true,
    editorValid: false,
    blocked: true,
  },
  play: async ({ args, canvas, userEvent }) => {
    await expect(
      canvas.getByRole("button", { name: "Done editing" })
    ).toHaveAttribute("aria-expanded", "true");
    await expect(
      canvas.getByRole("button", { name: "Save changes" })
    ).toBeDisabled();
    await expect(
      canvas.getByRole("button", { name: "Ask Hearth to change" })
    ).toBeDisabled();
    await userEvent.click(
      canvas.getByRole("button", { name: "Discard changes" })
    );
    await expect(args.onDiscard).toHaveBeenCalledOnce();
  },
};

export const Expanded: Story = {
  args: { expanded: true },
  play: async ({ args, canvas, userEvent }) => {
    const collapse = canvas.getByRole("button", {
      name: "Close expanded canvas",
    });
    await expect(collapse).toHaveAttribute("aria-expanded", "true");
    await userEvent.click(collapse);
    await expect(args.onToggleExpanded).toHaveBeenCalledOnce();
  },
};

/** A definition this version cannot read keeps its header, with every edit blocked. */
export const UnavailableView: Story = {
  args: {
    view: readDefinition({ title: "Future view" }),
    draft: { ...saved, definition: { title: "Future view" } },
    blocked: true,
  },
  play: async ({ canvas }) => {
    await expect(
      canvas.getByRole("heading", { name: "Unavailable view" })
    ).toBeVisible();
    await expect(canvas.getByRole("button", { name: "Edit" })).toBeDisabled();
    await expect(
      canvas.getByRole("button", { name: "Save as new" })
    ).toBeDisabled();
  },
};

/** On phones, Expand hides and the save actions become a bar along the bottom edge. */
export const PhoneUnsavedChanges: Story = {
  args: { draft: edited, unsaved: true },
  globals: { viewport: { value: "phone", isRotated: false } },
  play: async ({ canvas, canvasElement }) => {
    await expect(
      canvas.queryByRole("button", { name: "Expand" })
    ).not.toBeInTheDocument();
    await expect(
      canvas.getByRole("button", { name: "Save changes" })
    ).toBeVisible();
    await expectNoHorizontalOverflow(canvasElement);
  },
};
