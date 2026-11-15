import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, waitFor, within } from "storybook/test";
import { expectNoHorizontalOverflow } from "../../../.storybook/play.ts";
import { Dialog as HouseholdDialog } from "../../features/dashboards/Dialog.tsx";
import { CardSource } from "../../features/views/cards/CardSource.tsx";
import { Button } from "./button.tsx";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogTitle, DialogTrigger } from "./dialog.tsx";

const meta = {
  component: Dialog,
  parameters: { docs: { description: { component: "A centered decision dialog, or a bottom sheet on phones. Radix manages focus and dismissal." } } },
} satisfies Meta<typeof Dialog>;
export default meta;
type Story = StoryObj<typeof meta>;

function Decision({ placement = "center" }: { placement?: "center" | "bottom" }) {
  return <Dialog>
    <DialogTrigger asChild><Button variant="outline">Discard changes</Button></DialogTrigger>
    <DialogContent placement={placement}>
      <DialogTitle>Discard changes?</DialogTitle>
      <DialogDescription>Unsaved changes will be lost.</DialogDescription>
      <DialogFooter>
        <DialogClose asChild><Button variant="outline">Keep editing</Button></DialogClose>
        <DialogClose asChild><Button>Discard</Button></DialogClose>
      </DialogFooter>
    </DialogContent>
  </Dialog>;
}

export const Keyboard: Story = {
  render: () => <Decision />,
  play: async ({ canvas, canvasElement, userEvent }) => {
    const trigger = canvas.getByRole("button", { name: "Discard changes" });
    await userEvent.click(trigger);
    const body = within(canvasElement.ownerDocument.body);
    const dialog = await body.findByRole("dialog", { name: "Discard changes?" });
    await waitFor(() => expect(dialog.contains(canvasElement.ownerDocument.activeElement)).toBe(true));
    const first = within(dialog).getByRole("button", { name: "Keep editing" });
    const last = within(dialog).getByRole("button", { name: "Discard" });
    last.focus();
    await userEvent.tab();
    await expect(first).toHaveFocus();
    await userEvent.tab({ shift: true });
    await expect(last).toHaveFocus();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(body.queryByRole("dialog", { name: "Discard changes?" })).not.toBeInTheDocument());
    await expect(trigger).toHaveFocus();
  },
};

export const Centered: Story = {
  args: { children: <>
    <DialogTrigger asChild><Button variant="outline">Discard changes</Button></DialogTrigger>
    <DialogContent>
      <DialogTitle>Discard changes?</DialogTitle>
      <DialogDescription>Unsaved changes will be lost.</DialogDescription>
      <DialogFooter>
        <DialogClose asChild><Button variant="outline">Keep editing</Button></DialogClose>
        <DialogClose asChild><Button>Discard</Button></DialogClose>
      </DialogFooter>
    </DialogContent>
  </> },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Discard changes" }));
    await expect(await within(canvasElement.ownerDocument.body).findByRole("dialog", { name: "Discard changes?" })).toBeVisible();
  },
};

export const BottomOnDesktop: Story = {
  render: () => <Decision placement="bottom" />,
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Discard changes" }));
    const dialog = await within(canvasElement.ownerDocument.body).findByRole("dialog", { name: "Discard changes?" });
    const rect = dialog.getBoundingClientRect();
    await expect(Math.abs((rect.top + rect.bottom) / 2 - window.innerHeight / 2)).toBeLessThan(1);
  },
};

export const PhoneSheet: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  render: () => <Decision placement="bottom" />,
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Discard changes" }));
    const dialog = await within(canvasElement.ownerDocument.body).findByRole("dialog", { name: "Discard changes?" });
    const rect = dialog.getBoundingClientRect();
    const style = getComputedStyle(dialog);
    await expect(rect.bottom).toBe(window.innerHeight);
    await expect(rect.left).toBe(0);
    await expect(rect.width).toBe(window.innerWidth);
    await expect(style.borderTopLeftRadius).toBe("14px");
    await expect(style.borderTopRightRadius).toBe("14px");
    await expect(style.borderBottomLeftRadius).toBe("0px");
    await expectNoHorizontalOverflow(canvasElement);
  },
};

/** Temporary reference: the actual dashboard dialog and its legacy CSS, until D10. */
export const HouseholdComparison: Story = {
  render: function Comparison() {
    const [open, setOpen] = useState(false);
    return <div className="hearth-app">
      <Button variant="outline" onClick={() => setOpen(true)}>Legacy dialog</Button>
      {open && <HouseholdDialog title="Legacy decision" onClose={() => setOpen(false)}>
        <p>Unsaved changes will be lost.</p>
        <div className="workspace-actions"><button type="button" onClick={() => setOpen(false)}>Keep editing</button></div>
      </HouseholdDialog>}
      <Decision />
    </div>;
  },
  play: async ({ canvas, canvasElement, userEvent }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Legacy dialog" }));
    const body = within(canvasElement.ownerDocument.body);
    const legacy = await body.findByRole("dialog", { name: "Legacy decision" });
    const properties = ["width", "backgroundColor", "borderColor", "borderWidth", "borderStyle", "borderRadius", "padding", "fontFamily", "fontSize"] as const;
    const expected = Object.fromEntries(properties.map(property => [property, getComputedStyle(legacy)[property]]));
    const shadow = getComputedStyle(legacy).boxShadow;
    await userEvent.click(within(legacy).getByRole("button", { name: "Keep editing" }));
    await userEvent.click(canvas.getByRole("button", { name: "Discard changes" }));
    const shared = await body.findByRole("dialog", { name: "Discard changes?" });
    for (const property of properties) await expect(getComputedStyle(shared)[property], property).toBe(expected[property]);
    await expect(getComputedStyle(shared).boxShadow).toContain(shadow);
  },
};

/** Temporary reference: the actual Source sheet and its legacy CSS, until D7. */
export const HouseholdSheetComparison: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  render: () => <>
    <CardSource title="Living room" observedLabel="Observed" now={0} sources={[]} />
    <Decision placement="bottom" />
  </>,
  play: async ({ canvas, canvasElement, userEvent }) => {
    const body = within(canvasElement.ownerDocument.body);
    await userEvent.click(canvas.getByRole("button", { name: "Source" }));
    const legacy = await body.findByRole("dialog", { name: "Source details" });
    const properties = ["width", "maxHeight", "backgroundColor", "color", "borderRadius", "padding", "fontFamily", "fontSize", "lineHeight"] as const;
    const expected = Object.fromEntries(properties.map(property => [property, getComputedStyle(legacy)[property]]));
    const handle = getComputedStyle(legacy, "::before");
    const handleProperties = ["width", "height", "margin", "borderRadius", "backgroundColor"] as const;
    const expectedHandle = Object.fromEntries(handleProperties.map(property => [property, handle[property]]));
    await userEvent.click(within(legacy).getByRole("button", { name: "Close details" }));
    await userEvent.click(canvas.getByRole("button", { name: "Discard changes" }));
    const shared = await body.findByRole("dialog", { name: "Discard changes?" });
    for (const property of properties) await expect(getComputedStyle(shared)[property], property).toBe(expected[property]);
    for (const property of handleProperties) await expect(getComputedStyle(shared, "::before")[property], `handle ${property}`).toBe(expectedHandle[property]);
    await expectNoHorizontalOverflow(canvasElement);
  },
};
