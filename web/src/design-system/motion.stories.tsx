import type { Meta, StoryObj } from "@storybook/react-vite";
import {
  CheckIcon,
  ChevronRightIcon,
  CircleDashedIcon,
  CopyIcon,
  RotateCcwIcon,
} from "lucide-react";
import {
  addTransitionType,
  startTransition,
  useId,
  useState,
  ViewTransition,
} from "react";
import type { ReactNode } from "react";
import { expect, userEvent, waitFor } from "storybook/test";

import { Button } from "../components/button";
import { Card } from "../components/card";
import { Switch } from "../components/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "../components/table";
import { runTravel, TravelingView } from "../lib/canvas-travel";
import { prefersReducedMotion } from "../lib/motion";
import { TokenValue, useLiveStyle } from "./live-style";
import { MOTION_CURVES, MOTION_DURATIONS } from "./tokens";

/**
 * The motion patterns from DESIGN.md, rendered with the live CSS. Each pattern
 * story replays its motion; component stories tagged `motion` show the patterns
 * in place.
 */
const meta = {
  title: "Design system/Motion",
  tags: ["ai-generated", "!autodocs", "!manifest", "motion"],
} satisfies Meta;
export default meta;
type Story = StoryObj<typeof meta>;

/** Literal class names, so Tailwind compiles every token the page reads. */
const DURATION_CLASS = {
  feedback: "duration-feedback",
  state: "duration-state",
  layout: "duration-layout",
  travel: "duration-travel",
} as const;
const CURVE_CLASS = {
  "out-expo": "ease-out-expo",
  pulse: "ease-pulse",
} as const;

/** `0.14s` becomes `140ms`. */
const milliseconds = (value: string) =>
  value.endsWith("ms")
    ? value
    : `${Math.round(Number(value.slice(0, -1)) * 1000)}ms`;

const TimingRow = ({
  name,
  className,
  use,
  read,
}: {
  name: string;
  className: string;
  use: string;
  read: (style: CSSStyleDeclaration) => string;
}) => {
  const [ref, value] = useLiveStyle<HTMLSpanElement>((_, style) => read(style));
  return (
    <TableRow>
      <TableCell>
        <code className="text-foreground">{name}</code>
        <span ref={ref} className={`transition-opacity ${className}`} />
      </TableCell>
      <TableCell>
        <TokenValue value={value} token={name} />
      </TableCell>
      <TableCell className="text-muted-foreground">{use}</TableCell>
    </TableRow>
  );
};

/** Every duration and curve, read from the element that uses its utility. */
export const Timing: Story = {
  render: () => (
    <Table aria-label="Motion timing">
      <TableHeader>
        <TableRow>
          <TableHead>Utility</TableHead>
          <TableHead>Value</TableHead>
          <TableHead>Used for</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {MOTION_DURATIONS.map(({ token, use }) => (
          <TimingRow
            key={token}
            name={DURATION_CLASS[token]}
            className={DURATION_CLASS[token]}
            use={use}
            read={(style) => milliseconds(style.transitionDuration)}
          />
        ))}
        {MOTION_CURVES.map(({ token, use }) => (
          <TimingRow
            key={token}
            name={CURVE_CLASS[token]}
            className={CURVE_CLASS[token]}
            use={use}
            read={(style) => style.transitionTimingFunction}
          />
        ))}
      </TableBody>
    </Table>
  ),
  play: async ({ canvasElement }) => {
    const value = (token: string) =>
      canvasElement.querySelector(`[data-token="${token}"]`);
    await waitFor(() =>
      expect(value("duration-feedback")).toHaveTextContent("140ms")
    );
    await expect(value("duration-state")).toHaveTextContent("180ms");
    await expect(value("duration-layout")).toHaveTextContent("240ms");
    await expect(value("duration-travel")).toHaveTextContent("360ms");
    await expect(value("ease-out-expo")).toHaveTextContent(
      "cubic-bezier(0.16, 1, 0.3, 1)"
    );
    await expect(value("ease-pulse")).toHaveTextContent(
      "cubic-bezier(0.45, 0, 0.55, 1)"
    );
  },
};

/** Remounts its stage on Replay, so each pattern can run again. */
const Replay = ({
  pattern,
  children,
}: {
  pattern: string;
  children: (run: number) => ReactNode;
}) => {
  const [run, setRun] = useState(0);
  return (
    <div className="grid max-w-md gap-4">
      <div key={run} data-slot="motion-stage" className="grid min-h-24 gap-3">
        {children(run)}
      </div>
      <Button
        variant="outline"
        className="justify-self-start"
        onClick={() => setRun((count) => count + 1)}
      >
        <RotateCcwIcon aria-hidden="true" />
        Replay {pattern}
      </Button>
    </div>
  );
};

const stageOf = (canvasElement: HTMLElement) =>
  canvasElement.querySelector<HTMLElement>('[data-slot="motion-stage"]');

/** Replays the pattern and waits for a keyframe animation with that name. */
const expectReplayRuns = async (
  canvasElement: HTMLElement,
  pattern: string,
  animationName: string,
  reducedAnimation?: string
) => {
  const replay = [...canvasElement.querySelectorAll("button")].find(
    (button) => button.textContent === `Replay ${pattern}`
  );
  await expect(replay).toBeDefined();
  if (replay) {
    await userEvent.click(replay);
  }
  const expected = prefersReducedMotion() ? reducedAnimation : animationName;
  const names = () =>
    stageOf(canvasElement)
      ?.getAnimations({ subtree: true })
      .map((animation) =>
        animation instanceof CSSAnimation ? animation.animationName : ""
      );
  if (expected) {
    await waitFor(() => expect(names()).toContain(expected));
    return;
  }
  await expect(names()).toEqual([]);
};

/** An element entering a sequence rises 8px; a block inside an arriving group rises 4px. */
export const Arrive: Story = {
  tags: ["motion"],
  render: () => (
    <Replay pattern="Arrive">
      {() => (
        <>
          <Card
            data-reduced-motion="fade"
            className="animate-arrive motion-reduce:animate-fade"
          >
            An element entering a sequence
          </Card>
          <p
            data-reduced-motion="fade"
            className="animate-arrive motion-reduce:animate-fade text-label text-muted-foreground [--motion-rise:4px]"
          >
            A block inside the arriving group
          </p>
        </>
      )}
    </Replay>
  ),
  play: ({ canvasElement }) =>
    expectReplayRuns(canvasElement, "Arrive", "motion-arrive", "motion-fade"),
};

const RESOLVE_TEXT =
  "Text that arrives in pieces resolves from a slight blur, one piece at a time.";

/** Each piece of arriving text fades in from a 3px blur. */
export const Resolve: Story = {
  tags: ["motion"],
  render: () => (
    <Replay pattern="Resolve">
      {() => (
        <p className="text-body text-gray-9 proportional-nums">
          {RESOLVE_TEXT.split(" ").map((word, index) => (
            <span
              // Words repeat, so the position identifies each piece.
              // oxlint-disable-next-line react/no-array-index-key
              key={index}
              data-reduced-motion="fade"
              className="animate-resolve motion-reduce:animate-fade"
              style={{ animationDelay: `${index * 60}ms` }}
            >
              {word}{" "}
            </span>
          ))}
        </p>
      )}
    </Replay>
  ),
  play: ({ canvasElement }) =>
    expectReplayRuns(canvasElement, "Resolve", "motion-resolve", "motion-fade"),
};

/** A disclosure grows to its content height while fading in; closing reverses it. */
export const Grow: Story = {
  tags: ["motion"],
  render: () => (
    <details
      data-reduced-motion="fade"
      className="group/grow text-label text-muted-foreground details-content:duration-state open:details-content:duration-layout max-w-md details-content:h-0 details-content:overflow-clip details-content:opacity-0 details-content:transition-[height,opacity,content-visibility] details-content:transition-discrete open:details-content:h-auto open:details-content:opacity-100 motion-reduce:details-content:transition-[opacity,content-visibility]"
    >
      <summary className="hover:text-foreground flex min-h-7 w-fit cursor-pointer list-none items-center gap-2 rounded-md [&::-webkit-details-marker]:hidden">
        Used 2 tools
        <ChevronRightIcon
          className="size-3.5 transition-transform group-open/grow:rotate-90"
          aria-hidden="true"
        />
      </summary>
      <ul className="border-border ms-[7.5px] mt-1 list-none border-s ps-[15.5px]">
        <li className="py-1">find_entities · complete</li>
        <li className="py-1">read_history · complete</li>
      </ul>
    </details>
  ),
  play: async ({ canvas, canvasElement }) => {
    const details = canvasElement.querySelector("details");
    await userEvent.click(canvas.getByText("Used 2 tools"));
    await expect(details).toHaveAttribute("open");
    await waitFor(() =>
      expect(canvas.getByText("read_history · complete")).toBeVisible()
    );
    await userEvent.click(canvas.getByText("Used 2 tools"));
    await expect(details).not.toHaveAttribute("open");
  },
};

/** A panel fades in while moving from the edge it opens from: 16px at layout duration for the canvas. */
export const Slide: Story = {
  tags: ["motion"],
  render: () => (
    <Replay pattern="Slide">
      {() => (
        <Card className="animate-canvas-in">
          A panel sliding in from the right
        </Card>
      )}
    </Replay>
  ),
  play: ({ canvasElement }) =>
    expectReplayRuns(canvasElement, "Slide", "canvas-in"),
};

const MorphSample = () => {
  const id = useId();
  const [ready, setReady] = useState(false);
  const commit = () => {
    if (prefersReducedMotion() || !document.startViewTransition) {
      setReady(true);
      return;
    }
    startTransition(() => {
      addTransitionType("hearth-morph");
      setReady(true);
    });
  };
  return (
    <div className="grid min-h-44 max-w-md content-between gap-4">
      {ready ? (
        <ViewTransition
          key="ready"
          name={id}
          default="none"
          share="hearth-morph-card"
        >
          <Card className="text-label">
            <p>Living room</p>
            <p className="text-title">21.5 °C</p>
          </Card>
        </ViewTransition>
      ) : (
        <ViewTransition
          key="pending"
          name={id}
          default="none"
          share="hearth-morph-card"
        >
          <Card className="text-label text-muted-foreground w-2/3">
            Preparing a view…
          </Card>
        </ViewTransition>
      )}
      <Button
        variant="outline"
        className="justify-self-start"
        onClick={ready ? () => setReady(false) : commit}
      >
        {ready ? "Reset placeholder" : "Commit view"}
      </Button>
    </div>
  );
};

export const Morph: Story = {
  render: () => <MorphSample />,
  play: async ({ canvas }) => {
    await userEvent.click(canvas.getByRole("button", { name: "Commit view" }));
    await expect(await canvas.findByText("21.5 °C")).toBeVisible();
    await userEvent.click(
      canvas.getByRole("button", { name: "Reset placeholder" })
    );
    await expect(canvas.getByText("Preparing a view…")).toBeVisible();
  },
};

const TravelCards = ({ id }: { id: string }) => (
  <TravelingView artifactId={id}>
    <div className="text-label grid gap-2">
      <Card>Living room · 21.5 °C</Card>
      <Card>Hallway lamp · On</Card>
    </div>
  </TravelingView>
);

const TravelSample = () => {
  const id = useId();
  const [open, setOpen] = useState(false);
  return (
    <div className="grid max-w-xl gap-4">
      <div className="grid grid-cols-2 gap-3">
        <section
          aria-label="Conversation"
          className="border-border rounded-card grid min-w-0 content-start gap-2 border p-3"
        >
          <p className="text-label text-muted-foreground">Conversation</p>
          {!open && <TravelCards id={id} />}
        </section>
        <section
          aria-label="Canvas"
          className="border-border rounded-card grid min-w-0 content-start gap-2 border p-3"
        >
          <p className="text-label text-muted-foreground">Canvas</p>
          {open && <TravelCards id={id} />}
        </section>
      </div>
      <Button
        variant="outline"
        className="justify-self-start"
        onClick={() => runTravel(() => setOpen(!open), !open)}
      >
        {open ? "Return to conversation" : "Move to canvas"}
      </Button>
    </div>
  );
};

export const Travel: Story = {
  render: () => <TravelSample />,
  play: async ({ canvas }) => {
    await userEvent.click(
      canvas.getByRole("button", { name: "Move to canvas" })
    );
    await waitFor(() =>
      expect(canvas.getByRole("region", { name: "Canvas" })).toHaveTextContent(
        "Hallway lamp · On"
      )
    );
    await userEvent.click(
      canvas.getByRole("button", { name: "Return to conversation" })
    );
    await waitFor(() =>
      expect(
        canvas.getByRole("region", { name: "Conversation" })
      ).toHaveTextContent("Hallway lamp · On")
    );
  },
};

/** While an outcome is awaited, a dot breathes on the pending pulse and a dashed icon turns at a constant rate. */
export const Pending: Story = {
  tags: ["motion"],
  render: () => (
    <div className="text-label text-gray-9 grid gap-3">
      <p className="flex items-center gap-2">
        <span
          aria-hidden="true"
          data-slot="pending-dot"
          className="bg-info animate-pending size-1.5 rounded-full"
        />
        Turning on…
      </p>
      <p className="flex items-center gap-2">
        <CircleDashedIcon
          aria-hidden="true"
          data-slot="pending-icon"
          className="animate-turn size-4"
        />
        read_history · outcome unknown or pending
      </p>
    </div>
  ),
  play: async ({ canvasElement }) => {
    const names = (slot: string) =>
      canvasElement
        .querySelector(`[data-slot="${slot}"]`)
        ?.getAnimations()
        .map((animation) =>
          animation instanceof CSSAnimation ? animation.animationName : ""
        );
    await expect(names("pending-dot")).toEqual(
      prefersReducedMotion() ? [] : ["view-pending"]
    );
    await expect(names("pending-icon")).toEqual(
      prefersReducedMotion() ? [] : ["motion-turn"]
    );
  },
};

const ConfirmButton = () => {
  const [confirmed, setConfirmed] = useState(false);
  const icon =
    "transition-[opacity,scale,filter] duration-feedback motion-reduce:transition-opacity motion-reduce:scale-100 motion-reduce:blur-none [grid-area:1/1]";
  return (
    <Button
      variant="ghost"
      size="touch"
      data-confirmed={confirmed || undefined}
      className="group/confirm data-[confirmed]:text-success justify-self-start px-2"
      aria-label={confirmed ? "Response copied" : "Copy response"}
      onClick={() => setConfirmed(true)}
    >
      <span className="grid">
        <CopyIcon
          aria-hidden="true"
          data-reduced-motion="fade"
          className={`${icon} group-data-[confirmed]/confirm:scale-50 group-data-[confirmed]/confirm:opacity-0`}
        />
        <CheckIcon
          aria-hidden="true"
          data-reduced-motion="fade"
          className={`${icon} scale-50 opacity-0 blur-[2px] group-data-[confirmed]/confirm:scale-100 group-data-[confirmed]/confirm:opacity-100 group-data-[confirmed]/confirm:blur-none`}
        />
      </span>
      {confirmed ? "Copied" : "Copy"}
    </Button>
  );
};

/** An icon swap: the old icon shrinks and fades as the new one grows in from a slight blur. */
export const Confirm: Story = {
  tags: ["motion"],
  render: () => <Replay pattern="Confirm">{() => <ConfirmButton />}</Replay>,
  play: async ({ canvas }) => {
    await userEvent.click(
      canvas.getByRole("button", { name: "Copy response" })
    );
    await expect(
      canvas.getByRole("button", { name: "Response copied" })
    ).toHaveTextContent("Copied");
  },
};

/** A line spreads from its center to full width; the afterglow then fades it over 10 seconds. */
export const Ignite: Story = {
  tags: ["motion"],
  render: () => (
    <Replay pattern="Ignite">
      {() => (
        <Card className="relative overflow-hidden">
          <span
            aria-hidden="true"
            className="animate-arrival bg-orange pointer-events-none absolute inset-x-0 top-0 h-0.5"
          />
          A changed reading lights the top edge
        </Card>
      )}
    </Replay>
  ),
  play: ({ canvasElement }) =>
    expectReplayRuns(canvasElement, "Ignite", "motion-ignite"),
};

/**
 * A whole-card press target shrinks to 98.5% while pressed. Synthetic pointer
 * events do not set :active, so press it by hand.
 */
export const Give: Story = {
  tags: ["motion"],
  render: () => (
    <Card asChild>
      <button
        type="button"
        className="text-label duration-feedback w-60 text-start transition-[scale] motion-safe:active:scale-[0.985]"
      >
        Press and hold this card
      </button>
    </Card>
  ),
};

/** A refused control shakes 3px sideways, one and a half times. */
export const Refuse: Story = {
  tags: ["motion"],
  render: () => (
    <Replay pattern="Refuse">
      {(run) => (
        <p className="text-label text-gray-9 flex items-center gap-3">
          <span
            className={run > 0 ? "animate-refuse inline-flex" : "inline-flex"}
          >
            <Switch aria-label="Ceiling light" size="touch" />
          </span>
          <span className="text-destructive">Rejected.</span>
        </p>
      )}
    </Replay>
  ),
  play: ({ canvasElement }) =>
    expectReplayRuns(canvasElement, "Refuse", "motion-refuse"),
};
