import type { z } from "zod";

import { Button } from "../../../../components/button";
import { NativeSelect } from "../../../../components/native-select";
import { stateBindingSchema } from "../../bindings/schema";
import {
  isQuantityField,
  quantityAxis,
  stateFields,
} from "../../bindings/state-fields";
import type { SeriesWindow } from "../../bindings/types";
import { EditorLabel } from "../../editor/editor-label";
import { FieldSelect } from "../../editor/field-select";
import { TextField } from "../../editor/text-field";
import type { SensorChoice } from "../time-series/editor";
import { MAX_RANKING_ROWS, rankingSchema } from "./schema";

const windows: [SeriesWindow, string][] = [
  ["1h", "Last hour"],
  ["6h", "Last 6 hours"],
  ["24h", "Last 24 hours"],
  ["7d", "Last 7 days"],
];
const MIN_ROWS = 2;

type Value = z.infer<typeof rankingSchema>;
type Source = Value["rows"][number]["source"];

/** A choice's first numeric field, as a source the card can bind. */
const sourceFor = (choice: SensorChoice): Source | undefined => {
  const entry = Object.entries(stateFields[choice.type] ?? {}).find(
    ([, field]) => isQuantityField(field)
  );
  return entry
    ? stateBindingSchema.parse({
        kind: "entity-state",
        entity_id: choice.id,
        entity_type: choice.type,
        field: entry[0] || undefined,
      })
    : undefined;
};

/** The canonical unit a row's field is stored in, or undefined when the Entity cannot say. */
const axisOf = (
  source: Source,
  choices: SensorChoice[]
): string | undefined => {
  try {
    return quantityAxis(
      source,
      choices.find((choice) => choice.id === source.entity_id)?.support
    );
  } catch {
    return undefined;
  }
};

/** Numeric Entities not yet ranked that share the ranking's canonical unit. */
const candidatesFor = (value: Value, choices: SensorChoice[]) => {
  const unit = axisOf(value.rows[0].source, choices);
  return choices.filter((choice) => {
    const source = sourceFor(choice);
    return (
      source !== undefined &&
      !value.rows.some(
        (row) =>
          row.source.entity_id === choice.id &&
          (row.source.field ?? "") === (source.field ?? "")
      ) &&
      axisOf(source, choices) === unit
    );
  });
};

export const RankingEditor = ({
  value,
  choices,
  onChange,
}: {
  value: Value;
  choices: SensorChoice[];
  onChange: (value: Value) => void;
}) => {
  const rowSchema = rankingSchema.shape.rows.element;
  const row = (index: number, next: Value["rows"][number]) =>
    onChange({
      ...value,
      rows: value.rows.map((entry, i) => (i === index ? next : entry)),
    });
  return (
    <>
      <TextField
        label="Ranking title"
        value={value.title}
        schema={rankingSchema.shape.title}
        errorMessage="Use 1–120 characters for the ranking title."
        onChange={(title) => onChange({ ...value, title })}
      />
      <EditorLabel>
        Show each row&apos;s range over
        <NativeSelect
          size="auto"
          value={value.range ?? ""}
          onChange={(event) =>
            onChange({
              ...value,
              range: event.target.value
                ? (event.target.value as SeriesWindow)
                : undefined,
            })
          }
        >
          <option value="">Current reading only</option>
          {windows.map(([windowValue, label]) => (
            <option key={windowValue} value={windowValue}>
              {label}
            </option>
          ))}
        </NativeSelect>
      </EditorLabel>
      {value.rows.map((item, index) => (
        <fieldset key={index}>
          <legend>Row {index + 1}</legend>
          <TextField
            label={`Row ${index + 1} label`}
            value={item.label ?? ""}
            placeholder={
              choices.find((choice) => choice.id === item.source.entity_id)
                ?.name ?? item.source.entity_id
            }
            optional
            schema={rowSchema.shape.label}
            errorMessage="Use at most 120 characters for the label."
            onChange={(label) =>
              row(index, { ...item, label: label || undefined })
            }
          />
          <FieldSelect
            source={item.source}
            onChange={(field) =>
              row(index, { ...item, source: { ...item.source, field } })
            }
          />
          <Button
            variant="outline"
            size="touch"
            type="button"
            disabled={value.rows.length <= MIN_ROWS}
            onClick={() =>
              onChange({
                ...value,
                rows: value.rows.filter((_, i) => i !== index),
              })
            }
          >
            Remove row {index + 1}
          </Button>
        </fieldset>
      ))}
      <EditorLabel>
        Add reading
        <NativeSelect
          size="auto"
          value=""
          disabled={value.rows.length >= MAX_RANKING_ROWS}
          onChange={(event) => {
            const choice = choices.find(
              (candidate) => candidate.id === event.target.value
            );
            const source = choice && sourceFor(choice);
            if (source) {
              onChange({ ...value, rows: [...value.rows, { source }] });
            }
          }}
        >
          <option value="">Choose a compatible sensor…</option>
          {candidatesFor(value, choices).map((choice) => (
            <option key={choice.id} value={choice.id}>
              {choice.name}
            </option>
          ))}
        </NativeSelect>
      </EditorLabel>
    </>
  );
};
