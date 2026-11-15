import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect } from "storybook/test";
import { Area, CartesianGrid, ComposedChart, Line, Tooltip, XAxis, YAxis } from "recharts";
import { ChartContainer } from "./chart";

const meta = {
  title: "Components/ChartContainer",
  component: ChartContainer,
  tags: ["ai-generated"],
  args: {
    className: "h-[230px] w-full min-w-0",
    children: <ComposedChart data={[
      { time: "06:00", mean: 20.1, band: [19.8, 20.4] },
      { time: "07:00", mean: 20.6, band: [20.3, 20.9] },
      { time: "08:00", mean: 21.1, band: [20.8, 21.4] },
      { time: "09:00", mean: 21.3, band: [21.0, 21.6] },
    ] satisfies Array<{ time: string; mean: number; band: [number, number] }>} margin={{ top: 8, right: 16, left: 0, bottom: 0 }} accessibilityLayer={false}>
      <CartesianGrid vertical={false} stroke="var(--gray-3)" />
      <XAxis dataKey="time" stroke="var(--gray-8)" tickLine={false} tickMargin={8} />
      <YAxis domain={[19, 23]} width={48} stroke="var(--gray-8)" tickLine={false} axisLine={false} />
      <Tooltip content={({ active, payload }) => {
        const sample = payload?.[0]?.payload as { time: string; mean: number; band: [number, number] } | undefined;
        return active && sample ? <div className="rounded-md border border-gray-4 bg-gray-2 px-2.5 py-2 text-label leading-normal text-foreground tabular-nums">
          <p className="text-muted-foreground">{sample.time}</p>
          <p>Living room avg {sample.mean.toFixed(1)} °C ({sample.band[0].toFixed(1)}–{sample.band[1].toFixed(1)})</p>
        </div> : null;
      }} />
      <Area type="linear" dataKey="band" stroke="none" fill="var(--chart-1)" fillOpacity={0.12} isAnimationActive={false} />
      <Line type="linear" dataKey="mean" stroke="var(--chart-1)" strokeWidth={2} dot={{ r: 2, strokeWidth: 0 }} isAnimationActive={false} />
    </ComposedChart>,
  },
  render: args => <div><div className="max-w-[640px]">
    <ul className="mb-2 flex list-none gap-4 text-label leading-normal">
      <li><span className="me-1.5 inline-block size-2.5 rounded-[2px] bg-chart-1" aria-hidden="true" />Living room</li>
    </ul>
    <div role="region" tabIndex={0} aria-label="Living room temperature, average line with minimum to maximum band">
      <p className="sr-only">Living room: latest 21.3 °C, range 19.8 to 21.6 °C.</p>
      <ChartContainer {...args} aria-hidden="true" />
    </div>
  </div></div>,
} satisfies Meta<typeof ChartContainer>;
export default meta;
type Story = StoryObj<typeof meta>;

export const TemperatureHistory: Story = {
  play: async ({ canvas, userEvent }) => {
    const region = canvas.getByRole("region", { name: "Living room temperature, average line with minimum to maximum band" });
    await expect(region).toHaveTextContent("Living room: latest 21.3 °C");
    await expect(canvas.getByText("Living room", { exact: true })).toBeVisible();
    const plot = region.querySelector("svg");
    if (!plot) throw new Error("The chart did not render");
    const bounds = plot.getBoundingClientRect();
    await expect(bounds.width).toBeGreaterThan(0);
    await expect(bounds.height).toBeGreaterThan(0);
    await userEvent.pointer({ target: plot, coords: { clientX: bounds.left + bounds.width / 2, clientY: bounds.top + bounds.height / 2 } });
    await expect(await canvas.findByText(/Living room avg/)).toBeVisible();
  },
};
