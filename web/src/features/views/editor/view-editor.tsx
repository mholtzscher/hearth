import { useEffect, useState } from "react";
import { z } from "zod";

import { apiFetch, useBaseUrlVersion } from "../../../api/client";
import { Button } from "../../../components/button";
import { NativeSelect } from "../../../components/native-select";
import { quantitySchema } from "../bindings/quantity";
import { fieldTypes } from "../bindings/state-fields";
import { AirPurifierEditor } from "../cards/air-purifier/editor";
import { CoverEditor } from "../cards/cover/editor";
import { DeviceEditor } from "../cards/device/editor";
import { ForecastEditor } from "../cards/forecast/editor";
import { forecastTypes } from "../cards/forecast/schema";
import { MetricEditor } from "../cards/metric/editor";
import { OutsideNowEditor } from "../cards/outside-now/editor";
import { PressureEditor } from "../cards/pressure/editor";
import { RainEditor } from "../cards/rain/editor";
import { ReadingListEditor } from "../cards/reading-list/editor";
import { StateTimelineEditor } from "../cards/state-timeline/editor";
import { StateEditor } from "../cards/state/editor";
import { headingSchema, noteSchema } from "../cards/text/schema";
import { TimeSeriesEditor } from "../cards/time-series/editor";
import type { SensorChoice } from "../cards/time-series/editor";
import { ToggleListEditor } from "../cards/toggle-list/editor";
import { ToggleTileEditor } from "../cards/toggle-tile/editor";
import { WeekPatternEditor } from "../cards/week-pattern/editor";
import { WindEditor } from "../cards/wind/editor";
import { definitionSchema } from "../catalog";
import type { ViewDefinition, ViewElement } from "../catalog";
import { parseDefinition } from "../catalog/compatibility";
import { gridSchema } from "../layout/schema";
import { EditorLabel } from "./editor-label";
import { TextField } from "./text-field";

const entityPage = z.object({
  items: z.array(
    z.object({
      id: z.string(),
      name: z.string(),
      type: z.string(),
      support: z.unknown().optional(),
      quantities: z.record(z.string(), quantitySchema).optional(),
      device_id: z.string().optional(),
    })
  ),
  next_cursor: z.string().nullish(),
});
const devicePage = z.object({
  items: z.array(z.object({ id: z.string(), name: z.string() })),
  next_cursor: z.string().nullish(),
});
interface Page<T> {
  items: T[];
  next_cursor?: string | null;
}
const readable = new Set<string>([
  ...fieldTypes,
  forecastTypes.hourly,
  forecastTypes.daily,
]);
const EMPTY_CHOICES: SensorChoice[] = [];
type LayoutElement = Extract<
  ViewElement,
  { type: "Heading" | "Note" | "Grid" | "Stack" }
>;
type CardElement = Exclude<ViewElement, LayoutElement>;

const fetchPages = async <T,>(
  path: string,
  signal: AbortSignal,
  decode: (text: string) => Page<T>,
  cursor?: string
): Promise<T[]> => {
  const query = new URLSearchParams({
    limit: "200",
    ...(cursor ? { cursor } : {}),
  });
  const page = await apiFetch<Page<T>>(`${path}?${query}`, { signal }, decode);
  const next = page.next_cursor ?? undefined;
  if (!next || signal.aborted) {
    return page.items;
  }
  return [...page.items, ...(await fetchPages(path, signal, decode, next))];
};

const LayoutChildren = ({
  node,
  onChange,
}: {
  node: Extract<ViewElement, { type: "Grid" | "Stack" }>;
  onChange: (value: ViewElement) => void;
}) => (
  <ol>
    {node.children.map((child, index) => (
      <li key={child}>
        {child}{" "}
        <Button
          variant="outline"
          size="icon-touch"
          type="button"
          disabled={index === 0}
          aria-label={`Move ${child} up`}
          onClick={() => {
            const children = [...node.children];
            [children[index - 1], children[index]] = [
              children[index],
              children[index - 1],
            ];
            onChange({ ...node, children });
          }}
        >
          ↑
        </Button>
        <Button
          variant="outline"
          size="icon-touch"
          type="button"
          disabled={index === node.children.length - 1}
          aria-label={`Move ${child} down`}
          onClick={() => {
            const children = [...node.children];
            [children[index], children[index + 1]] = [
              children[index + 1],
              children[index],
            ];
            onChange({ ...node, children });
          }}
        >
          ↓
        </Button>
      </li>
    ))}
  </ol>
);

const CardElementEditor = ({
  node,
  choices,
  onChange,
}: {
  node: CardElement;
  choices: SensorChoice[];
  onChange: (value: ViewElement) => void;
}) => {
  switch (node.type) {
    case "Metric": {
      return (
        <MetricEditor
          value={node.props}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "State": {
      return (
        <StateEditor
          value={node.props}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "ReadingList": {
      return (
        <ReadingListEditor
          value={node.props}
          choices={choices}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "Device": {
      return (
        <DeviceEditor
          value={node.props}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "StateTimeline": {
      return (
        <StateTimelineEditor
          value={node.props}
          choices={choices}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "WeekPattern": {
      return (
        <WeekPatternEditor
          value={node.props}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "ToggleTile": {
      return (
        <ToggleTileEditor
          value={node.props}
          choices={choices}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "ToggleList": {
      return (
        <ToggleListEditor
          value={node.props}
          choices={choices}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "AirPurifier": {
      return (
        <AirPurifierEditor
          value={node.props}
          choices={choices}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "Cover": {
      return (
        <CoverEditor
          value={node.props}
          choices={choices}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "OutsideNow": {
      return (
        <OutsideNowEditor
          value={node.props}
          choices={choices}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "Rain": {
      return (
        <RainEditor
          value={node.props}
          choices={choices}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "Wind": {
      return (
        <WindEditor
          value={node.props}
          choices={choices}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "Pressure": {
      return (
        <PressureEditor
          value={node.props}
          choices={choices}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "Forecast": {
      return (
        <ForecastEditor
          value={node.props}
          choices={choices}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    case "TimeSeries": {
      return (
        <TimeSeriesEditor
          value={node.props}
          choices={choices}
          onChange={(props) => onChange({ ...node, props })}
        />
      );
    }
    default: {
      return null;
    }
  }
};

const ElementEditor = ({
  node,
  choices,
  onChange,
}: {
  node: ViewElement;
  choices: SensorChoice[];
  onChange: (value: ViewElement) => void;
}) => {
  if (node.type === "Heading" || node.type === "Note") {
    return (
      <TextField
        label="Text"
        multiline
        value={node.props.text}
        onChange={(text) => onChange({ ...node, props: { text } })}
        schema={
          node.type === "Heading"
            ? headingSchema.shape.text
            : noteSchema.shape.text
        }
        errorMessage={`Use 1–${node.type === "Heading" ? 120 : 500} characters for this ${node.type.toLowerCase()}.`}
      />
    );
  }
  if (node.type === "Grid") {
    return (
      <>
        <EditorLabel>
          Columns
          <NativeSelect
            size="auto"
            value={node.props.columns}
            onChange={(event) =>
              onChange({
                ...node,
                props: {
                  columns: gridSchema.shape.columns.parse(
                    Number(event.target.value)
                  ),
                },
              })
            }
          >
            <option>1</option>
            <option>2</option>
            <option>3</option>
            <option>4</option>
          </NativeSelect>
        </EditorLabel>
        <LayoutChildren node={node} onChange={onChange} />
      </>
    );
  }
  if (node.type === "Stack") {
    return <LayoutChildren node={node} onChange={onChange} />;
  }
  return (
    <CardElementEditor node={node} choices={choices} onChange={onChange} />
  );
};

export const ViewEditor = ({
  definition,
  onChange,
  onValidityChange,
}: {
  definition: ViewDefinition;
  onChange: (definition: ViewDefinition) => void;
  onValidityChange?: (valid: boolean) => void;
}) => {
  const [draft, setDraft] = useState({
    source: definition,
    working: definition,
  });
  if (draft.source !== definition) {
    setDraft({ source: definition, working: definition });
  }
  const working = draft.source === definition ? draft.working : definition;
  useEffect(() => {
    onValidityChange?.(definition.schema_version === 1);
  }, [definition, onValidityChange]);
  const [choicesState, setChoicesState] = useState<{
    key: string;
    choices: SensorChoice[];
  } | null>(null);
  const [error, setError] = useState("");
  const version = useBaseUrlVersion();
  // Entity names repeat across Devices ("Humidity"), so cards that pick sources by name also show the Device.
  const deviceNames = Object.values(definition.spec.elements).some((node) =>
    [
      "ToggleTile",
      "ToggleList",
      "AirPurifier",
      "Cover",
      "OutsideNow",
      "Rain",
      "Wind",
      "Pressure",
      "Forecast",
    ].includes(node.type)
  );
  const choicesKey = `${version}:${deviceNames}`;
  const choices =
    choicesState?.key === choicesKey ? choicesState.choices : EMPTY_CHOICES;
  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const entityItems = await fetchPages(
          "/v1/entities",
          controller.signal,
          (text) => entityPage.parse(JSON.parse(text))
        );
        const all: SensorChoice[] = entityItems.filter((item) =>
          readable.has(item.type)
        );
        if (deviceNames) {
          const devices = await fetchPages(
            "/v1/devices",
            controller.signal,
            (text) => devicePage.parse(JSON.parse(text))
          );
          const names = new Map(
            devices.map((device) => [device.id, device.name])
          );
          for (const choice of all) {
            if (choice.device_id) {
              choice.device_name = names.get(choice.device_id);
            }
          }
        }
        if (!controller.signal.aborted) {
          setChoicesState({ key: choicesKey, choices: all });
        }
      } catch {
        if (!controller.signal.aborted) {
          setError(
            "Sensor list unavailable; existing selections are preserved."
          );
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [choicesKey, deviceNames]);
  const update = (candidate: ViewDefinition) => {
    setDraft({ source: definition, working: candidate });
    const parsed = parseDefinition(candidate);
    onValidityChange?.(parsed !== null);
    if (parsed) {
      setError("");
      onChange(parsed);
    } else {
      setError(
        "This edit exceeds the view limits or leaves required text empty."
      );
    }
  };
  const element = (key: string, value: ViewElement) => {
    update({
      ...working,
      spec: {
        ...working.spec,
        elements: { ...working.spec.elements, [key]: value },
      },
    });
  };
  const validation = definitionSchema.safeParse(working);
  const invalidNodes = new Set(
    validation.success
      ? []
      : validation.error.issues.flatMap((issue) =>
          issue.path[0] === "spec" && issue.path[1] === "elements"
            ? [String(issue.path[2])]
            : []
        )
  );
  return (
    <div className="text-label grid gap-3 leading-normal">
      <p>Changes stay in this draft until you save.</p>
      {error && <output className="block">{error}</output>}
      <TextField
        label="Dashboard title"
        value={working.title}
        onChange={(title) => update({ ...working, title })}
        schema={definitionSchema.shape.title}
        errorMessage="Use 1–80 characters for the dashboard title."
      />
      {Object.entries(working.spec.elements).map(([key, node]) => (
        <details key={key} className="border-border border-t pt-2">
          <summary className="min-h-11 cursor-pointer content-center">
            {node.type} · {key}
            {invalidNodes.has(key) && (
              <span className="text-destructive"> · Fix invalid fields</span>
            )}
          </summary>
          <ElementEditor
            node={node}
            choices={choices}
            onChange={(value) => element(key, value)}
          />
        </details>
      ))}
    </div>
  );
};
