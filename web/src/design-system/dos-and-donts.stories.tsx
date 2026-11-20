import { Description, Stories, Title } from "@storybook/addon-docs/blocks";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { CheckIcon, XIcon } from "lucide-react";
import { useState } from "react";
import type { ReactNode } from "react";
import { expect } from "storybook/test";

import { entityId, observedAt } from "../../.storybook/fixtures";
import { expectNoHorizontalOverflow } from "../../.storybook/play";
import { Button } from "../components/button";
import { Card } from "../components/card";
import { Facts } from "../components/facts";
import { SegmentedControl } from "../components/segmented-control";
import { DataCard, StatusList } from "../features/views/cards/data-card";
import { RangeTrack } from "../features/views/cards/metric/range-track";
import { Reading, ReadingUnit } from "../features/views/cards/reading";
import { statusEntry } from "../features/views/cards/status";
import { controlStatusClasses } from "../features/views/controls/control-status";
import { StatusDot } from "../features/views/controls/status-dot";
import { cn } from "../lib/utils";
import { TokenValue, useLiveStyle } from "./live-style";

const meta = {
  title: "Design system/Do's and Don'ts",
  tags: ["ai-generated", "!manifest"],
  parameters: {
    docs: {
      description: {
        component:
          "Each rule from DESIGN.md beside its mistake, built from the real tokens and components.",
      },
      canvas: { sourceState: "none" },
      // One page of pairs; no separate primary story or controls.
      page: () => (
        <>
          <Title />
          <Description />
          <Stories title="" includePrimary />
        </>
      ),
    },
  },
} satisfies Meta;
export default meta;
type Story = StoryObj<typeof meta>;

const Verdict = ({ good }: { good: boolean }) => (
  <span
    className={cn(
      "text-label inline-flex items-center gap-1.5 font-semibold",
      good ? "text-success" : "text-destructive"
    )}
  >
    {good ? (
      <CheckIcon aria-hidden="true" className="size-4" />
    ) : (
      <XIcon aria-hidden="true" className="size-4" />
    )}
    {good ? "Do" : "Don't"}
  </span>
);

const Side = ({
  good,
  rule,
  children,
}: {
  good: boolean;
  rule: string;
  children: ReactNode;
}) => (
  <Card
    size="compact"
    data-verdict={good ? "do" : "dont"}
    className={cn("grid content-start gap-3", !good && "border-dashed")}
  >
    <div className="grid gap-0.5">
      <Verdict good={good} />
      <p className="text-foreground m-0">{rule}</p>
    </div>
    <div className="flex min-w-0 flex-wrap items-center gap-3">{children}</div>
  </Card>
);

const Pair = ({
  doRule,
  doExample,
  dontRule,
  dontExample,
}: {
  doRule: string;
  doExample: ReactNode;
  dontRule: string;
  dontExample: ReactNode;
}) => (
  <div className="tablet:grid-cols-2 grid gap-3">
    <Side good rule={doRule}>
      {doExample}
    </Side>
    <Side good={false} rule={dontRule}>
      {dontExample}
    </Side>
  </div>
);

const sources = [
  {
    entityId: entityId(15),
    label: "Living room",
    availability: "available" as const,
    enabled: true,
  },
];

const NeutralSurfacesPair = () => (
  <Pair
    doRule="Use the neutral gray ramp for surfaces on a true black ground."
    doExample={
      <Card size="compact" className="min-w-40">
        Living room · 21.5 °C
      </Card>
    }
    dontRule="Tint the gray ramp."
    dontExample={
      <Card
        size="compact"
        className="min-w-40 border-[color-mix(in_oklch,var(--gray-3),var(--info)_35%)] bg-[color-mix(in_oklch,var(--gray-1),var(--info)_18%)]"
      >
        Living room · 21.5 °C
      </Card>
    }
  />
);

const PurifierMode = () => {
  const [mode, setMode] = useState("auto");
  return (
    <SegmentedControl
      label="Purifier mode"
      value={mode}
      options={[
        { value: "auto", label: "Auto" },
        { value: "sleep", label: "Sleep" },
      ]}
      onValueChange={setMode}
    />
  );
};

/** The gray ramp is untinted; the ground is true black. */
export const NeutralSurfaces: Story = {
  name: "Neutral surfaces",
  render: () => <NeutralSurfacesPair />,
};

const SignalsWithTextPair = () => (
  <Pair
    doRule="Pair arrival, status, and availability signals with explicit text."
    doExample={
      <>
        <StatusList entries={[statusEntry("unavailable")]} />
        <span className="text-muted-foreground">Observed 3h ago</span>
      </>
    }
    dontRule="Depend on color alone for meaning."
    dontExample={
      <span className="inline-flex items-center gap-2">
        <StatusDot tone="warn" />
        <span className="text-foreground">Living room</span>
      </span>
    }
  />
);

/** A status pairs its tone with words. */
export const SignalsWithText: Story = {
  name: "Signals with text",
  render: () => <SignalsWithTextPair />,
};

const VoiceAndActionPair = () => (
  <Pair
    doRule="Use violet for selection and Hearth's voice, orange for the main action."
    doExample={
      <>
        <PurifierMode />
        <Button>Send</Button>
      </>
    }
    dontRule="Use orange for selection, or a brand hue for status."
    dontExample={
      <>
        <Button
          variant="outline"
          aria-pressed="true"
          className="border-orange text-orange"
        >
          Auto
        </Button>
        <span className="text-violet inline-flex items-center gap-1.5">
          <span
            aria-hidden="true"
            className="bg-violet size-1.5 rounded-full"
          />
          Unavailable
        </span>
      </>
    }
  />
);

/** The One Action Rule: violet is Hearth's voice, orange the one action. */
export const VoiceAndAction: Story = {
  name: "Voice and action",
  render: () => <VoiceAndActionPair />,
};

const OneActionPair = () => (
  <Pair
    doRule="Use outline buttons for paging; a disabled primary turns gray."
    doExample={
      <>
        <Button variant="outline">Next page</Button>
        <Button disabled>Save dashboard</Button>
      </>
    }
    dontRule="Turn orange into a series color or a dimmed orange primary."
    dontExample={
      <>
        <svg width="96" height="32" viewBox="0 0 96 32" aria-hidden="true">
          <path
            d="M0 24 L24 16 L48 20 L72 6 L96 12"
            fill="none"
            stroke="var(--orange)"
            strokeWidth="2"
          />
        </svg>
        <Button
          disabled
          className="disabled:border-primary disabled:bg-primary disabled:text-primary-foreground disabled:opacity-50"
        >
          Save dashboard
        </Button>
      </>
    }
  />
);

/** Orange is never a series color, and a disabled primary turns gray. */
export const OneAction: Story = {
  name: "One action",
  render: () => <OneActionPair />,
};

const GlossaryTermsPair = () => (
  <Pair
    doRule="Name State, Entity availability, and Command outcome precisely."
    doExample={
      <Facts
        rows={[
          ["State", "On"],
          ["Entity availability", "Available"],
          ["Command outcome", "Rejected"],
        ]}
      />
    }
    dontRule="Blur them into one vague status."
    dontExample={<span className="text-foreground">Light: Error</span>}
  />
);

/** State, Entity availability, and Command outcome are separate facts. */
export const GlossaryTerms: Story = {
  name: "Glossary terms",
  render: () => <GlossaryTermsPair />,
};

const ReserveStatusLinePair = () => (
  <Pair
    doRule="Reserve a control's status line while idle."
    doExample={
      <div className="grid min-w-40 gap-0">
        <span className="text-foreground">Hall light · Off</span>
        <div
          className={cn(controlStatusClasses, "outline-gray-5 outline-dashed")}
          title="Reserved status line"
        />
      </div>
    }
    dontRule="Insert the status line only when there is something to say."
    dontExample={
      <div className="grid min-w-40">
        <span className="text-foreground">Hall light · Off</span>
      </div>
    }
  />
);

/** The Stable Height Rule. */
export const ReserveStatusLine: Story = {
  name: "Reserved status line",
  render: () => <ReserveStatusLinePair />,
};

const MeasuredButton = ({
  className,
  size,
}: {
  className?: string;
  size?: "touch";
}) => {
  const [ref, height] = useLiveStyle<HTMLButtonElement>(
    (_element, style) => style.height
  );
  return (
    <>
      <Button ref={ref} variant="outline" size={size} className={className}>
        Turn all off
      </Button>
      <TokenValue value={height} token={size ?? "small"} />
    </>
  );
};

const TouchTargetsPair = () => (
  <Pair
    doRule="Give household controls the touch size."
    doExample={<MeasuredButton size="touch" />}
    dontRule="Shrink a household control below the touch size."
    dontExample={<MeasuredButton className="text-label h-6 px-2" />}
  />
);

/** Household controls take a 44px target on touch. */
export const TouchTargets: Story = {
  name: "Touch targets",
  render: () => <TouchTargetsPair />,
  play: async ({ canvasElement }) => {
    await expect(
      canvasElement.querySelector('[data-token="touch"]')
    ).toHaveTextContent("44px");
  },
};

const SharedFramePair = () => (
  <Pair
    doRule="Build a new data card on the shared frame and card states."
    doExample={
      <DataCard
        title="Rain"
        status={[]}
        observedAt={observedAt}
        sources={sources}
        className="min-w-48 flex-1"
      >
        <Reading className="text-reading-narrow">
          2.4<ReadingUnit>mm</ReadingUnit>
        </Reading>
      </DataCard>
    }
    dontRule="Invent a custom frame for a card type."
    dontExample={
      <div className="grid min-w-48 flex-1 gap-1 rounded-[20px] bg-[linear-gradient(135deg,color-mix(in_oklch,var(--info)_30%,var(--gray-1)),var(--gray-1))] p-4">
        <span className="text-label tracking-[.1em] uppercase">Rain</span>
        <span className="text-reading-narrow font-bold">2.4mm</span>
      </div>
    }
  />
);

/** The One Frame Rule. */
export const SharedFrame: Story = {
  name: "Shared frame",
  render: () => <SharedFramePair />,
};

const LitCard = ({ children }: { children: ReactNode }) => (
  <Card size="compact" className="relative min-w-40 overflow-hidden">
    <span
      aria-hidden="true"
      className="bg-orange absolute inset-x-0 top-0 h-0.5"
    />
    {children}
  </Card>
);

const AfterglowForChangesPair = () => (
  <Pair
    doRule="Light the afterglow only when a reading changes."
    doExample={<LitCard>Living room · 21.6 °C</LitCard>}
    dontRule="Use it for age, availability, or a successful Command."
    dontExample={<LitCard>Hall light · Turned on</LitCard>}
  />
);

/** The Evidence Rule. */
export const AfterglowForChanges: Story = {
  name: "Afterglow for changes",
  render: () => <AfterglowForChangesPair />,
};

const RangesAndGradientsPair = () => (
  <Pair
    doRule="Show a range on a thin neutral track with words."
    doExample={
      <div className="min-w-48 flex-1">
        <RangeTrack
          dimmed={false}
          data={{
            value: 21.5,
            observed_at: observedAt,
            format: { unit_label: "°C", fraction_digits: 1 },
            range: { minimum: 5, maximum: 30 },
          }}
        />
      </div>
    }
    dontRule="Draw a radial gauge, a now marker, or a decorative gradient."
    dontExample={
      <svg width="96" height="56" viewBox="0 0 96 56" aria-hidden="true">
        <defs>
          <linearGradient id="dont-gauge">
            <stop offset="0" stopColor="var(--ok)" />
            <stop offset="1" stopColor="var(--error)" />
          </linearGradient>
        </defs>
        <path
          d="M8 50 A40 40 0 0 1 88 50"
          fill="none"
          stroke="url(#dont-gauge)"
          strokeWidth="8"
        />
        <line
          x1="48"
          y1="50"
          x2="68"
          y2="22"
          stroke="var(--gray-10)"
          strokeWidth="2"
        />
      </svg>
    }
  />
);

/** Ranges use a thin neutral track; surfaces stay flat. */
export const RangesAndGradients: Story = {
  name: "Ranges, gauges, and gradients",
  render: () => <RangesAndGradientsPair />,
};

const FocusRingPair = () => (
  <Pair
    doRule="Keep the one 2px violet focus ring with its 3px offset."
    doExample={
      <Button
        variant="outline"
        className="outline-ring outline-2 outline-offset-3"
      >
        Filter
      </Button>
    }
    dontRule="Restyle the focus ring."
    dontExample={
      <Button variant="outline" className="ring-info ring-3">
        Filter
      </Button>
    }
  />
);

/** The One Focus Ring Rule; both buttons draw their ring here without focus. */
export const FocusRing: Story = {
  name: "Focus ring",
  render: () => <FocusRingPair />,
};

/** Every pair fits a phone. */
export const Phone: Story = {
  tags: ["!autodocs"],
  globals: { viewport: { value: "phone", isRotated: false } },
  render: () => (
    <div className="grid gap-4">
      <NeutralSurfacesPair />
      <SignalsWithTextPair />
      <VoiceAndActionPair />
      <OneActionPair />
      <GlossaryTermsPair />
      <ReserveStatusLinePair />
      <TouchTargetsPair />
      <SharedFramePair />
      <AfterglowForChangesPair />
      <RangesAndGradientsPair />
      <FocusRingPair />
    </div>
  ),
  play: ({ canvasElement }) => expectNoHorizontalOverflow(canvasElement),
};
