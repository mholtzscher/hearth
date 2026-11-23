import { useEffect, useRef, useState } from "react";
import type { MouseEvent } from "react";

import { cn } from "../../../lib/utils";
import { DataCard } from "./data-card";
import type { DataCardProps } from "./data-card";

export type TileFrameProps = Omit<DataCardProps, "compactTimeLabel"> & {
  on: boolean;
  interactive: boolean;
  /** A request can be pending without moving the reading or selected surface. */
  working?: boolean;
  /** Evidence time for power or position, not the oldest metered reading. */
  stateObservedAt?: string;
  metered?: boolean;
};

const percent = (offset: number, extent: number) =>
  Math.max(0, Math.min(100, (offset / extent) * 100));

const rememberPress = (event: MouseEvent<HTMLElement>) => {
  if (
    !(event.target instanceof Element) ||
    !event.target.closest('[data-slot="tile-action"]')
  ) {
    return;
  }
  const tile = event.currentTarget;
  if (!event.detail) {
    tile.style.removeProperty("--tile-origin");
    return;
  }
  const { x, y, width, height } = tile.getBoundingClientRect();
  const across = percent(event.clientX - x, width);
  const down = percent(event.clientY - y, height);
  tile.style.setProperty("--tile-origin", `${across}% ${down}%`);
};

/** The shared whole-card press layout. */
export const TileFrame = ({
  on,
  interactive,
  working = false,
  stateObservedAt,
  metered,
  className,
  classNames,
  ...props
}: TileFrameProps) => {
  const [previous, setPrevious] = useState({
    on,
    stateObservedAt,
    spread: false,
  });
  if (on !== previous.on || stateObservedAt !== previous.stateObservedAt) {
    setPrevious({
      on,
      stateObservedAt,
      spread:
        on &&
        (previous.on
          ? previous.spread
          : Date.parse(stateObservedAt ?? "") >
            Date.parse(previous.stateObservedAt ?? "")),
    });
  }
  const pressed = useRef<HTMLElement | null>(null);
  useEffect(() => {
    if (!working && !on) {
      pressed.current?.style.removeProperty("--tile-origin");
    }
  }, [working, on]);
  return (
    <DataCard
      {...props}
      compactTimeLabel
      data-on={on || undefined}
      data-reduced-motion="fade"
      onClickCapture={
        interactive
          ? (event) => {
              pressed.current = event.currentTarget;
              rememberPress(event);
            }
          : undefined
      }
      className={cn(
        "has-[[data-slot=tile-action]:focus-visible]:outline-ring duration-state @container isolate transition-[border-color] has-[[data-slot=tile-action]:focus-visible]:outline-2 has-[[data-slot=tile-action]:focus-visible]:outline-offset-3 data-[on=true]:border-[color-mix(in_oklch,var(--violet)_38%,var(--gray-3))]",
        "before:duration-state before:pointer-events-none before:absolute before:inset-0 before:-z-10 before:bg-[color-mix(in_oklch,var(--violet)_11%,var(--gray-1))] before:opacity-0 before:transition-opacity data-[on=true]:before:opacity-100 motion-safe:data-[on=true]:before:transition-none",
        previous.spread && "motion-safe:before:animate-spread",
        interactive &&
          "hover:border-gray-6 data-[on=true]:hover:border-gray-6 motion-safe:has-[[data-slot=tile-action]:enabled:active]:duration-feedback motion-safe:transition-[border-color,scale] motion-safe:has-[[data-slot=tile-action]:enabled:active]:scale-[0.985]",
        metered && "max-tablet:col-span-full max-tablet:gap-2 max-tablet:p-4",
        className
      )}
      classNames={{
        body: cn(
          "flex flex-col gap-2",
          metered && "max-tablet:gap-1.5",
          classNames?.body
        ),
        footer: cn(
          "pointer-events-none relative z-[2] gap-2",
          classNames?.footer
        ),
        timeLabel: cn("@max-[200px]:hidden", classNames?.timeLabel),
        sourceToggle: cn(
          "pointer-events-auto @max-[200px]:-me-1 @max-[200px]:px-1",
          classNames?.sourceToggle
        ),
      }}
    />
  );
};

export const tileActionClass =
  "block w-full border-0 bg-transparent p-0 text-left text-inherit after:absolute after:inset-0 after:z-[1] after:rounded-card focus-visible:outline-none disabled:opacity-100";
