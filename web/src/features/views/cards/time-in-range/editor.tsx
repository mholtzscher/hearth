import { useState } from "react";
import type { z } from "zod";

import { Button } from "../../../../components/button";
import { Input } from "../../../../components/input";
import { NativeSelect } from "../../../../components/native-select";
import { quantityField } from "../../bindings/state-fields";
import type { SeriesWindow } from "../../bindings/types";
import { EditorLabel } from "../../editor/editor-label";
import { FieldSelect } from "../../editor/field-select";
import { TextField } from "../../editor/text-field";
import { timeInRangeSchema } from "./schema";

const windows: [SeriesWindow, string][] = [
  ["1h", "Last hour"],
  ["6h", "Last 6 hours"],
  ["24h", "Last 24 hours"],
  ["7d", "Last 7 days"],
];

type Value = z.infer<typeof timeInRangeSchema>;

/** The unit thresholds are written in: named by the catalog, or the Entity's own when its support declares it. */
const unitWords = (source: Value["source"]): string => {
  const unit = quantityField(source)?.quantity.unit;
  return typeof unit === "string" ? unit : "the sensor's own unit";
};

/** A number field that keeps intermediate text such as "-" or "2." while it is typed. */
const ThresholdInput = ({
  label,
  value,
  onChange,
}: {
  label: string;
  value: number;
  onChange: (value: number) => void;
}) => {
  const [text, setText] = useState(String(value));
  const [synced, setSynced] = useState(value);
  if (value !== synced) {
    setSynced(value);
    if (Number(text) !== value) {
      setText(String(value));
    }
  }
  const invalid = text.trim() === "" || !Number.isFinite(Number(text));
  return (
    <EditorLabel>
      {label}
      <Input
        size="auto"
        inputMode="decimal"
        value={text}
        aria-invalid={invalid || undefined}
        onChange={(event) => {
          setText(event.target.value);
          const next = Number(event.target.value);
          if (event.target.value.trim() !== "" && Number.isFinite(next)) {
            setSynced(next);
            onChange(next);
          }
        }}
      />
      {invalid && <span className="text-destructive">Enter a number.</span>}
    </EditorLabel>
  );
};

const ascending = (thresholds: number[]): boolean =>
  thresholds.length < 2 || thresholds[0] < thresholds[1];

export const TimeInRangeEditor = ({
  value,
  onChange,
}: {
  value: Value;
  onChange: (value: Value) => void;
}) => {
  const unit = unitWords(value.source);
  const setThreshold = (index: number, next: number) =>
    onChange({
      ...value,
      thresholds: value.thresholds.map((entry, i) =>
        i === index ? next : entry
      ),
    });
  return (
    <>
      <TextField
        label="Label"
        value={value.label ?? ""}
        optional
        schema={timeInRangeSchema.shape.label}
        errorMessage="Use at most 120 characters for the label."
        onChange={(label) => onChange({ ...value, label: label || undefined })}
      />
      <FieldSelect
        source={value.source}
        onChange={(field) =>
          onChange({ ...value, source: { ...value.source, field } })
        }
      />
      <EditorLabel>
        Time window
        <NativeSelect
          size="auto"
          value={value.window}
          onChange={(event) =>
            onChange({ ...value, window: event.target.value as SeriesWindow })
          }
        >
          {windows.map(([windowValue, label]) => (
            <option key={windowValue} value={windowValue}>
              {label}
            </option>
          ))}
        </NativeSelect>
      </EditorLabel>
      <fieldset>
        <legend>Thresholds, in {unit}</legend>
        <ThresholdInput
          label={
            value.thresholds.length === 1
              ? `Threshold (${unit})`
              : `Lower threshold (${unit})`
          }
          value={value.thresholds[0]}
          onChange={(next) => setThreshold(0, next)}
        />
        {value.thresholds.length === 2 && (
          <ThresholdInput
            label={`Upper threshold (${unit})`}
            value={value.thresholds[1]}
            onChange={(next) => setThreshold(1, next)}
          />
        )}
        {!ascending(value.thresholds) && (
          <p className="text-destructive">
            The upper threshold must be above the lower one.
          </p>
        )}
        {value.thresholds.length === 1 ? (
          <Button
            variant="outline"
            size="touch"
            type="button"
            onClick={() =>
              onChange({
                ...value,
                thresholds: [
                  value.thresholds[0],
                  value.thresholds[0] +
                    Math.max(1, Math.abs(value.thresholds[0]) / 10),
                ],
              })
            }
          >
            Add an upper threshold
          </Button>
        ) : (
          <Button
            variant="outline"
            size="touch"
            type="button"
            onClick={() =>
              onChange({ ...value, thresholds: [value.thresholds[0]] })
            }
          >
            Remove the upper threshold
          </Button>
        )}
      </fieldset>
    </>
  );
};
