import { Component, useContext } from "react";
import type { CSSProperties, ReactNode } from "react";

import { Button } from "../../../components/button";
import { cn } from "../../../lib/utils";
import { BoundAirPurifier } from "../cards/air-purifier/air-purifier";
import { BoundCover } from "../cards/cover/cover";
import { DataCard, HeadingLevel } from "../cards/data-card";
import { BoundDevice } from "../cards/device/device";
import { BoundForecast } from "../cards/forecast/forecast";
import { BoundMetric } from "../cards/metric/metric";
import { BoundOutsideNow } from "../cards/outside-now/outside-now";
import { BoundPressure } from "../cards/pressure/pressure";
import { BoundRain } from "../cards/rain/rain";
import { BoundReadingList } from "../cards/reading-list/reading-list";
import { BoundStateTimeline } from "../cards/state-timeline/state-timeline";
import { BoundState } from "../cards/state/state";
import { BoundTimeSeries } from "../cards/time-series/time-series";
import { BoundToggleList } from "../cards/toggle-list/toggle-list";
import { BoundToggleTile } from "../cards/toggle-tile/toggle-tile";
import { BoundWeekPattern } from "../cards/week-pattern/week-pattern";
import { BoundWind } from "../cards/wind/wind";
import type { ViewElement } from "../catalog";
import { readDefinition } from "../catalog/compatibility";

/** A validated element graph. A null element is preserved content this version cannot render. */
interface Graph {
  root: string;
  elements: Record<string, ViewElement | null>;
  children: Record<string, string[]>;
}

type CardElement = Exclude<
  ViewElement,
  { type: "Stack" | "Grid" | "Heading" | "Note" }
>;

const stackGaps = {
  small: "[--stack-gap:8px]",
  medium: "[--stack-gap:16px]",
  large: "[--stack-gap:24px]",
} as const;

const cardDefaultTitles = {
  Metric: "Reading",
  State: "State",
  Device: "Device",
  WeekPattern: "State",
  ToggleTile: "Power",
  AirPurifier: "Air purifier",
  Cover: "Cover",
  OutsideNow: "Outside",
  Rain: "Rain",
  Wind: "Wind",
  Pressure: "Pressure",
  Forecast: "Forecast",
} as const;

/**
 * Quick switches and single readings fit narrower cells than other cards, so
 * grids made only of them keep more columns, including on phones.
 */
const isTile = (element: ViewElement | null | undefined) =>
  element?.type === "ToggleTile" ||
  ((element?.type === "AirPurifier" || element?.type === "Cover") &&
    element.props.layout === "tile");

const gridKind = (graph: Graph, id: string) => {
  const elements = graph.children[id].map((child) => graph.elements[child]);
  const types = elements.map((element) => element?.type);
  if (elements.every(isTile)) {
    return "toggle";
  }
  if (types.every((type) => type === "Metric" || type === "State")) {
    return "reading";
  }
};

/**
 * A four-column grid too narrow for four cards halves to two, so four cards
 * never wrap as three and one. Each breakpoint is four minimum-width cards plus
 * three 12px gaps: 4 × 248 + 36 and 4 × 160 + 36.
 */
const fourColumnFallback = {
  regular: "@max-[1028px]/grid:[--view-columns:2]",
  compact: "@max-[676px]/grid:[--view-columns:2]",
} as const;

const isCardElement = (node: ViewElement): node is CardElement =>
  node.type !== "Stack" &&
  node.type !== "Grid" &&
  node.type !== "Heading" &&
  node.type !== "Note";

const cardTitle = (node: CardElement): string => {
  if (
    node.type === "TimeSeries" ||
    node.type === "ReadingList" ||
    node.type === "StateTimeline" ||
    node.type === "ToggleList"
  ) {
    return node.props.title;
  }
  return node.props.label ?? cardDefaultTitles[node.type];
};

const unsupportedStyles =
  "rounded-card border border-dashed border-gray-4 px-4 py-3.5 text-label text-muted-foreground";

interface CardBoundaryProps {
  resetKey: string;
  title: string;
  children: ReactNode;
}

interface CardBoundaryState {
  failed: boolean;
  resetKey: string;
}

/**
 * Keeps one failing card from blanking the whole view, and says so rather than
 * leaving a silent gap. A changed definition gets a fresh attempt.
 */
class CardBoundary extends Component<CardBoundaryProps, CardBoundaryState> {
  constructor(props: CardBoundaryProps) {
    super(props);
    this.state = { failed: false, resetKey: props.resetKey };
  }
  static getDerivedStateFromError() {
    return { failed: true };
  }
  static getDerivedStateFromProps(
    props: CardBoundaryProps,
    state: CardBoundaryState
  ): Partial<CardBoundaryState> | null {
    if (state.resetKey === props.resetKey) {
      return null;
    }
    return { failed: false, resetKey: props.resetKey };
  }
  render() {
    if (!this.state.failed) {
      return this.props.children;
    }
    return (
      <DataCard
        title={this.props.title}
        status={[
          { key: "error", tone: "error", text: "This card couldn't be shown" },
        ]}
        sources={[]}
      >
        <p className="text-label text-muted-foreground">
          Edit the view or ask Hearth to change it.
        </p>
      </DataCard>
    );
  }
}

const renderCard = (node: CardElement) => {
  const title = cardTitle(node);
  switch (node.type) {
    case "Metric": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundMetric {...node.props} />
        </CardBoundary>
      );
    }
    case "TimeSeries": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundTimeSeries {...node.props} />
        </CardBoundary>
      );
    }
    case "State": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundState {...node.props} />
        </CardBoundary>
      );
    }
    case "ReadingList": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundReadingList {...node.props} />
        </CardBoundary>
      );
    }
    case "Device": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundDevice {...node.props} />
        </CardBoundary>
      );
    }
    case "StateTimeline": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundStateTimeline {...node.props} />
        </CardBoundary>
      );
    }
    case "WeekPattern": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundWeekPattern {...node.props} />
        </CardBoundary>
      );
    }
    case "ToggleTile": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundToggleTile {...node.props} />
        </CardBoundary>
      );
    }
    case "ToggleList": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundToggleList {...node.props} />
        </CardBoundary>
      );
    }
    case "AirPurifier": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundAirPurifier {...node.props} />
        </CardBoundary>
      );
    }
    case "Cover": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundCover {...node.props} />
        </CardBoundary>
      );
    }
    case "OutsideNow": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundOutsideNow {...node.props} />
        </CardBoundary>
      );
    }
    case "Rain": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundRain {...node.props} />
        </CardBoundary>
      );
    }
    case "Wind": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundWind {...node.props} />
        </CardBoundary>
      );
    }
    case "Pressure": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundPressure {...node.props} />
        </CardBoundary>
      );
    }
    case "Forecast": {
      return (
        <CardBoundary resetKey={JSON.stringify(node.props)} title={title}>
          <BoundForecast {...node.props} />
        </CardBoundary>
      );
    }
    default: {
      return null;
    }
  }
};

const ViewNode = ({
  graph,
  id,
  followsHeading = false,
}: {
  graph: Graph;
  id: string;
  followsHeading?: boolean;
}) => {
  const node = graph.elements[id];
  const Heading = `h${useContext(HeadingLevel)}` as "h3";
  // Unsupported content stays unrendered with its descendants, so none of them start reads.
  if (!node) {
    return (
      <div className={unsupportedStyles}>
        Unsupported component · original content preserved
      </div>
    );
  }
  const children = graph.children[id].map((child, index, siblings) => (
    <ViewNode
      key={child}
      graph={graph}
      id={child}
      followsHeading={
        node.type === "Stack" &&
        index > 0 &&
        graph.elements[siblings[index - 1]]?.type === "Heading"
      }
    />
  ));
  if (isCardElement(node)) {
    return renderCard(node);
  }
  switch (node.type) {
    case "Stack": {
      return (
        <div
          className={`grid min-w-0 gap-(--stack-gap) ${stackGaps[node.props.gap]}`}
        >
          {children}
        </div>
      );
    }
    case "Grid": {
      const kind = gridKind(graph, id);
      const { columns } = node.props;
      return (
        <div
          className="@container/grid min-w-0"
          style={{ "--view-columns": columns } as CSSProperties}
        >
          <div
            data-slot="view-grid"
            data-grid-kind={kind}
            className={cn(
              "grid min-w-0 grid-cols-[repeat(auto-fit,minmax(min(100%,max(var(--view-min-card,248px),calc((100%_-_(var(--view-columns)_-_1)_*_12px)_/_var(--view-columns)))),1fr))] gap-3",
              kind ? "[--view-min-card:160px]" : "max-tablet:grid-cols-1",
              columns === 4 && fourColumnFallback[kind ? "compact" : "regular"]
            )}
          >
            {children}
          </div>
        </div>
      );
    }
    case "Heading": {
      return (
        <Heading className="text-title text-balance">{node.props.text}</Heading>
      );
    }
    case "Note": {
      return (
        <p
          className={`text-body text-muted-foreground max-w-[70ch] ${followsHeading ? "mt-[calc(4px-var(--stack-gap))]" : ""}`}
        >
          {node.props.text}
        </p>
      );
    }
    default: {
      return null;
    }
  }
};

export const DownloadDefinition = ({ raw }: { raw: unknown }) => (
  <Button
    variant="link"
    size="default"
    className="p-0 underline"
    onClick={() => {
      const url = URL.createObjectURL(
        new Blob([JSON.stringify(raw, null, 2)], { type: "application/json" })
      );
      const link = document.createElement("a");
      link.href = url;
      link.download = "hearth-view.json";
      link.click();
      URL.revokeObjectURL(url);
    }}
  >
    Download original definition
  </Button>
);
/** headingLevel is the level of the view's own Heading elements; the host's title sits one level above. Card titles use the next level. */
export const ViewRenderer = ({
  definition,
  active = true,
  headingLevel,
}: {
  definition: unknown;
  active?: boolean;
  headingLevel: 3 | 4;
}) => {
  const view = readDefinition(definition);
  if (view.status === "unavailable") {
    return (
      <div className={unsupportedStyles}>
        <p>{view.reason}</p>
        <DownloadDefinition raw={definition} />
      </div>
    );
  }
  if (!active) {
    return (
      <div className="text-foreground">
        {view.status === "supported" ? view.definition.title : view.title}
        <span className="text-label text-muted-foreground block">
          Open view to load live readings
        </span>
      </div>
    );
  }
  const graph: Graph =
    view.status === "partial"
      ? view
      : {
          root: view.definition.spec.root,
          elements: view.definition.spec.elements,
          children: Object.fromEntries(
            Object.entries(view.definition.spec.elements).map(([key, node]) => [
              key,
              "children" in node ? node.children : [],
            ])
          ),
        };
  return (
    <HeadingLevel.Provider value={headingLevel}>
      {view.status === "partial" && (
        <div className={unsupportedStyles}>
          Some content requires a newer Hearth version. Editing is disabled.{" "}
          <DownloadDefinition raw={definition} />
        </div>
      )}
      <ViewNode graph={graph} id={graph.root} />
    </HeadingLevel.Provider>
  );
};
