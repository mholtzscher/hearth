import type { Meta, StoryObj } from "@storybook/react-vite";
import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { expect, waitFor } from "storybook/test";

import { entityId, observedAt } from "../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../.storybook/play";
import { Button } from "../components/button";
import { Card } from "../components/card";
import { Switch } from "../components/switch";
import { DataCard } from "../features/views/cards/data-card";
import type { SourceItem } from "../features/views/cards/data-card";
import { Reading, ReadingUnit } from "../features/views/cards/reading";
import { statusEntry } from "../features/views/cards/status";
import type { StatusKey, ValueTreatment } from "../features/views/cards/status";
import {
  controlStatusClasses,
  controlStatusLineClasses,
} from "../features/views/controls/control-status";
import { StatusDot } from "../features/views/controls/status-dot";
import { cn } from "../lib/utils";
import { colorToHex, TokenValue } from "./live-style";

const meta = {
  title: "Design system/View cards",
  tags: ["ai-generated", "!autodocs", "!manifest"],
} satisfies Meta;
export default meta;
type Story = StoryObj<typeof meta>;

const sources: SourceItem[] = [
  {
    entityId: entityId(15),
    label: "Living room",
    availability: "available",
    enabled: true,
  },
];

const SLOTS = [
  {
    name: "Title",
    selector: '[data-slot="card-title"]',
    read: (style: CSSStyleDeclaration) =>
      `${style.fontSize} · ${style.fontWeight} · ${colorToHex(style.color)}`,
  },
  {
    name: "Unit",
    selector: '[data-slot="card-unit"]',
    read: (style: CSSStyleDeclaration) =>
      `${style.fontSize} · ${colorToHex(style.color)}`,
  },
  {
    name: "Status",
    selector: '[data-slot="card-status"]',
    read: (style: CSSStyleDeclaration) =>
      `${style.fontSize} · tone dot and words`,
  },
  {
    name: "Body",
    selector: '[data-slot="card-body"]',
    read: () => "A reading with its unit, or a chart",
  },
  {
    name: "Time",
    selector: '[data-slot="card-footer"] time',
    read: (style: CSSStyleDeclaration) =>
      `${style.fontSize} · ${colorToHex(style.color)} · tabular figures`,
  },
  {
    name: "Source",
    selector: '[data-slot="card-footer"] button',
    read: (style: CSSStyleDeclaration) =>
      `${style.fontSize} · ${colorToHex(style.color)}`,
  },
  {
    name: "Afterglow",
    selector: '[data-slot="card"]',
    read: () => "orange top edge on a changed reading",
  },
] as const;

interface Marker {
  name: string;
  top: number;
  left: number;
  width: number;
  height: number;
  value: string;
}

/** Measures each slot of the real DataCard and labels it in place. */
const FrameAnatomyFigure = () => {
  const stage = useRef<HTMLDivElement>(null);
  const [markers, setMarkers] = useState<Marker[]>([]);
  const [frame, setFrame] = useState<string>();
  useLayoutEffect(() => {
    const root = stage.current;
    if (!root) {
      return;
    }
    const measure = () => {
      const origin = root.getBoundingClientRect();
      setMarkers(
        SLOTS.flatMap(({ name, selector, read }) => {
          const element = root.querySelector<HTMLElement>(selector);
          if (!element) {
            return [];
          }
          const rect = element.getBoundingClientRect();
          const afterglow = name === "Afterglow";
          return [
            {
              name,
              top: rect.top - origin.top,
              left: rect.left - origin.left,
              width: rect.width,
              height: afterglow ? 2 : rect.height,
              value: read(getComputedStyle(element)),
            },
          ];
        })
      );
      const card = root.querySelector<HTMLElement>('[data-slot="card"]');
      if (card) {
        const style = getComputedStyle(card);
        setFrame(
          `${colorToHex(style.backgroundColor)} · ${style.borderTopWidth} ${colorToHex(style.borderTopColor)} · ${style.borderTopLeftRadius} corners · ${style.paddingTop} padding`
        );
      }
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(root);
    return () => observer.disconnect();
  }, []);
  return (
    <div className="desktop:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] grid items-start gap-6">
      <div ref={stage} className="relative px-3 py-4">
        <DataCard
          title="Living room"
          unit="°C"
          status={[statusEntry("refresh-failed")]}
          observedAt={observedAt}
          sources={sources}
        >
          <Reading>21.5</Reading>
        </DataCard>
        {markers.map((marker, index) => (
          <div
            key={marker.name}
            aria-hidden="true"
            className="pointer-events-none absolute"
            style={{
              top: marker.top,
              left: marker.left,
              width: marker.width,
              height: marker.height,
            }}
          >
            <span className="border-violet absolute -inset-1 rounded-md border border-dashed" />
            <span className="bg-violet text-gray-0 text-label absolute -top-3 -left-3 grid size-5 place-items-center rounded-full font-semibold">
              {index + 1}
            </span>
          </div>
        ))}
      </div>
      <div className="grid gap-3">
        <ol className="text-label m-0 grid list-none gap-2 p-0">
          {markers.map((marker, index) => (
            <li
              key={marker.name}
              data-slot-name={marker.name}
              className="grid grid-cols-[24px_minmax(0,1fr)] gap-x-2"
            >
              <span className="bg-violet text-gray-0 text-label grid size-5 place-items-center rounded-full font-semibold">
                {index + 1}
              </span>
              <span className="grid">
                <span className="text-foreground font-medium">
                  {marker.name}
                </span>
                <TokenValue
                  value={marker.value}
                  token={`slot-${marker.name}`}
                />
              </span>
            </li>
          ))}
        </ol>
        <p className="text-label text-muted-foreground m-0">
          Frame: <TokenValue value={frame} token="card-frame" />
        </p>
      </div>
    </div>
  );
};

export const FrameAnatomy: Story = {
  render: () => <FrameAnatomyFigure />,
  play: async ({ canvasElement }) => {
    // Every slot of the real frame was found and measured.
    await waitFor(() =>
      expect(canvasElement.querySelectorAll("[data-slot-name]")).toHaveLength(
        SLOTS.length
      )
    );
    const title = canvasElement.querySelector<HTMLElement>(
      '[data-slot="card-title"]'
    );
    if (!title) {
      throw new Error("The card title is missing.");
    }
    await expect(
      canvasElement.querySelector('[data-token="slot-Title"]')
    ).toHaveTextContent(getComputedStyle(title).fontSize);
  },
};

const STATES: {
  name: string;
  status: StatusKey[];
  value: ValueTreatment;
  observed: boolean;
  afterglow: string;
}[] = [
  {
    name: "Loading",
    status: ["loading"],
    value: "empty",
    observed: false,
    afterglow: "none",
  },
  {
    name: "Missing",
    status: ["missing"],
    value: "empty",
    observed: false,
    afterglow: "none",
  },
  {
    name: "Request error",
    status: ["error"],
    value: "empty",
    observed: false,
    afterglow: "none",
  },
  {
    name: "Unavailable",
    status: ["unavailable"],
    value: "dimmed",
    observed: true,
    afterglow: "none",
  },
  {
    name: "Disabled",
    status: ["disabled"],
    value: "dimmed",
    observed: true,
    afterglow: "none",
  },
  {
    name: "Refresh failed",
    status: ["refresh-failed"],
    value: "live",
    observed: true,
    afterglow: "on arrival",
  },
  {
    name: "Availability unknown",
    status: ["availability-unknown"],
    value: "live",
    observed: true,
    afterglow: "none",
  },
  {
    name: "Never observed",
    status: ["unobserved"],
    value: "empty",
    observed: false,
    afterglow: "none",
  },
  {
    name: "Absent",
    status: ["absent"],
    value: "empty",
    observed: true,
    afterglow: "none",
  },
  {
    name: "Ready and available",
    status: [],
    value: "live",
    observed: true,
    afterglow: "on arrival",
  },
];

const CardStateGrid = () => (
  <ul className="m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(min(100%,248px),1fr))] gap-3 p-0">
    {STATES.map((state) => (
      <li key={state.name} className="grid content-start gap-2">
        <p className="text-label text-muted-foreground m-0">
          <span className="text-foreground font-medium">{state.name}</span>
          {" · "}afterglow {state.afterglow}
        </p>
        <DataCard
          title="Living room"
          status={state.status.map((key) =>
            statusEntry(
              key,
              key === "error" ? "Hearth returned 503." : undefined
            )
          )}
          observedAt={state.observed ? observedAt : undefined}
          sources={sources}
          className="h-full"
        >
          <Reading value={state.value}>
            {state.value === "empty" ? (
              "—"
            ) : (
              <>
                21.5<ReadingUnit>°C</ReadingUnit>
              </>
            )}
          </Reading>
        </DataCard>
      </li>
    ))}
  </ul>
);

export const CardStates: Story = {
  render: () => <CardStateGrid />,
  play: async ({ canvas, canvasElement }) => {
    await expect(canvas.getAllByRole("article")).toHaveLength(STATES.length);
    // Every state but ready and available states itself in words.
    const statuses = [
      ...canvasElement.querySelectorAll('[data-slot="card-status"]'),
    ].map((list) => list.textContent);
    await expect(statuses).toHaveLength(STATES.length - 1);
    await expect(statuses).toContain(
      "Entity not found It may have been removed."
    );
  },
};

const reducedMotion = "(prefers-reduced-motion: reduce)";
const subscribeReducedMotion = (change: () => void) => {
  const media = window.matchMedia(reducedMotion);
  media.addEventListener("change", change);
  return () => media.removeEventListener("change", change);
};
const prefersReducedMotion = () => window.matchMedia(reducedMotion).matches;

/** The live animation on the card's afterglow, or undefined while it is dark. */
const readAfterglow = (card: HTMLElement): string | undefined => {
  const glow = card.querySelector<HTMLElement>('[data-slot="card-arrival"]');
  if (!glow) {
    return undefined;
  }
  const style = getComputedStyle(glow);
  return style.animationName === "none"
    ? "none: lit without fading"
    : `${style.animationName} · ${style.animationDuration} · ${style.animationTimingFunction}`;
};

const AfterglowDemo = () => {
  const [reading, setReading] = useState(() => ({
    count: 0,
    value: 21.5,
    start: Date.now(),
  }));
  const card = useRef<HTMLDivElement>(null);
  const [animation, setAnimation] = useState<string>();
  const reduced = useSyncExternalStore(
    subscribeReducedMotion,
    prefersReducedMotion
  );
  const updatedAt = new Date(
    reading.start + reading.count * 1000
  ).toISOString();
  const shown = reading.value.toFixed(1);
  // DataCard mounts a new afterglow element for each arrival and removes it when the glow ends.
  useEffect(() => {
    const root = card.current;
    if (!root) {
      return;
    }
    const observer = new MutationObserver(() =>
      setAnimation(readAfterglow(root))
    );
    observer.observe(root, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, []);
  return (
    <div className="desktop:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] grid items-start gap-6">
      <div ref={card}>
        <DataCard
          title="Living room"
          status={[]}
          observedAt={updatedAt}
          arrival={{ updatedAt, shown }}
          sources={sources}
        >
          <Reading>
            {shown}
            <ReadingUnit>°C</ReadingUnit>
          </Reading>
        </DataCard>
      </div>
      <div className="grid gap-3">
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            size="touch"
            onClick={() =>
              setReading((previous) => ({
                ...previous,
                count: previous.count + 1,
                value: Math.round((previous.value + 0.1) * 10) / 10,
              }))
            }
          >
            New reading
          </Button>
          <Button
            variant="outline"
            size="touch"
            onClick={() =>
              setReading((previous) => ({
                ...previous,
                count: previous.count + 1,
              }))
            }
          >
            Same value again
          </Button>
        </div>
        <p aria-live="polite" className="text-label text-muted-foreground m-0">
          Afterglow:{" "}
          <TokenValue value={animation ?? "dark"} token="afterglow" />
        </p>
        <p className="text-label text-muted-foreground m-0">
          Reduced motion is {reduced ? "on" : "off"} here.
        </p>
      </div>
    </div>
  );
};

export const Afterglow: Story = {
  render: () => <AfterglowDemo />,
  play: async ({ canvas, canvasElement, userEvent }) => {
    const glow = () =>
      canvasElement.querySelector<HTMLElement>('[data-slot="card-arrival"]');
    // Opening a view lights nothing, and the same value reported again stays dark.
    await expect(glow()).toBeNull();
    await userEvent.click(
      canvas.getByRole("button", { name: "Same value again" })
    );
    await expect(glow()).toBeNull();
    await userEvent.click(canvas.getByRole("button", { name: "New reading" }));
    await waitFor(() => expect(glow()).not.toBeNull());
    const reduced = window.matchMedia(
      "(prefers-reduced-motion: reduce)"
    ).matches;
    await expect(getComputedStyle(glow() as HTMLElement).animationName).toBe(
      reduced ? "none" : "motion-ignite, view-arrival"
    );
    await expect(
      canvasElement.querySelector('[data-token="afterglow"]')
    ).not.toHaveTextContent("dark");
  },
};

/** A light tile whose pending words either reserve their line or push the next card down. */
const LightTile = ({ reserved }: { reserved: boolean }) => {
  const [on, setOn] = useState(false);
  const [pending, setPending] = useState<boolean>();
  useEffect(() => {
    if (pending === undefined) {
      return;
    }
    const timer = setTimeout(() => {
      setOn(pending);
      setPending(undefined);
    }, 1500);
    return () => clearTimeout(timer);
  }, [pending]);
  const words =
    pending === undefined ? undefined : (
      <>
        <StatusDot tone="info" />
        {pending ? "Turning on…" : "Turning off…"}
      </>
    );
  const label = reserved ? "With a reserved line" : "Without a reserved line";
  return (
    <div className="grid content-start gap-3" data-variant={label}>
      <p className="text-label text-foreground m-0 font-medium">{label}</p>
      <DataCard title="Hall light" status={[]} sources={[]}>
        <div className="flex items-center justify-between gap-3">
          <Reading className="text-reading-narrow">{on ? "On" : "Off"}</Reading>
          <Switch
            size="touch"
            aria-label={`Hall light power, ${label.toLowerCase()}`}
            checked={pending ?? on}
            onCheckedChange={(checked) => setPending(checked)}
          />
        </div>
        {reserved ? (
          <div className={controlStatusClasses}>
            <output data-tone="info" className={controlStatusLineClasses}>
              {words}
            </output>
          </div>
        ) : (
          words && (
            <output
              data-tone="info"
              className={cn(
                "text-label text-gray-9 mt-2",
                controlStatusLineClasses
              )}
            >
              {words}
            </output>
          )
        )}
      </DataCard>
      <Card
        size="compact"
        data-slot="next-card"
        className="text-muted-foreground"
      >
        Next card
      </Card>
    </div>
  );
};

export const StableHeight: Story = {
  render: () => (
    <div className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,248px),1fr))] gap-6">
      <LightTile reserved={false} />
      <LightTile reserved />
    </div>
  ),
  play: async ({ canvas, canvasElement, userEvent }) => {
    const nextTop = (variant: string) =>
      canvasElement
        .querySelector(`[data-variant="${variant}"] [data-slot="next-card"]`)
        ?.getBoundingClientRect().top ?? Number.NaN;
    const before = {
      without: nextTop("Without a reserved line"),
      with: nextTop("With a reserved line"),
    };
    await userEvent.click(
      canvas.getByRole("switch", { name: /without a reserved line/u })
    );
    await userEvent.click(
      canvas.getByRole("switch", { name: /with a reserved line/u })
    );
    await expect(canvas.getAllByText("Turning on…")).toHaveLength(2);
    await expect(nextTop("Without a reserved line")).toBeGreaterThan(
      before.without
    );
    await expect(nextTop("With a reserved line")).toBe(before.with);
  },
};

/** The view card figures fit a phone. */
export const Phone: Story = {
  globals: { viewport: { value: "phone", isRotated: false } },
  render: () => (
    <div className="grid gap-8">
      <FrameAnatomyFigure />
      <CardStateGrid />
    </div>
  ),
  play: ({ canvasElement }) => expectNoHorizontalOverflow(canvasElement),
};
