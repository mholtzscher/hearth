import { cn } from "../../../lib/utils";
import { DataCard, type DataCardProps } from "./DataCard";

export type TileFrameProps = Omit<DataCardProps, "compactTimeLabel"> & {
  on: boolean;
  interactive: boolean;
  metered?: boolean;
};

/** The whole-card press layout shared by power, cover, and purifier tiles. */
export function TileFrame({ on, interactive, metered, className, classNames, ...props }: TileFrameProps) {
  return <DataCard {...props} compactTimeLabel data-on={on || undefined} className={cn(
    "@container transition-[background-color,border-color] duration-[180ms] ease-out-expo has-[[data-slot=tile-action]:focus-visible]:outline-2 has-[[data-slot=tile-action]:focus-visible]:outline-offset-3 has-[[data-slot=tile-action]:focus-visible]:outline-ring data-[on=true]:bg-[color-mix(in_oklch,var(--violet)_11%,var(--gray-1))] data-[on=true]:border-[color-mix(in_oklch,var(--violet)_38%,var(--gray-3))]",
    interactive && "hover:border-gray-6 data-[on=true]:hover:border-gray-6",
    metered && "max-tablet:col-span-full max-tablet:gap-2 max-tablet:p-4",
    className,
  )} classNames={{
    body: cn("flex flex-col gap-2", metered && "max-tablet:gap-1.5", classNames?.body),
    footer: cn("pointer-events-none relative z-[2] gap-2", classNames?.footer),
    timeLabel: cn("@max-[200px]:hidden", classNames?.timeLabel),
    sourceToggle: cn("pointer-events-auto @max-[200px]:-me-1 @max-[200px]:px-1", classNames?.sourceToggle),
  }} />;
}

export const tileActionClass = "block w-full border-0 bg-transparent p-0 text-left text-inherit after:absolute after:inset-0 after:z-[1] after:rounded-card focus-visible:outline-none disabled:opacity-100";
