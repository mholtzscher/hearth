import type { z } from "zod";

import { Button } from "../../../../components/button";
import { NativeSelect } from "../../../../components/native-select";
import { readingBindingSchema } from "../../bindings/schema";
import { stateFields } from "../../bindings/state-fields";
import { EditorLabel } from "../../editor/editor-label";
import { FieldSelect } from "../../editor/field-select";
import { TextField } from "../../editor/text-field";
import { WordsEditor } from "../state/editor";
import type { SensorChoice } from "../time-series/editor";
import { readingListSchema, MAX_LIST_ROWS } from "./schema";

export const ReadingListEditor = ({
  value,
  choices,
  onChange,
}: {
  value: z.infer<typeof readingListSchema>;
  choices: SensorChoice[];
  onChange: (value: z.infer<typeof readingListSchema>) => void;
}) => {
  const rowSchema = readingListSchema.shape.rows.element;
  const row = (index: number, next: z.infer<typeof rowSchema>) => {
    onChange({
      ...value,
      rows: value.rows.map((entry, i) => (i === index ? next : entry)),
    });
  };
  return (
    <>
      <TextField
        label="List title"
        value={value.title}
        schema={readingListSchema.shape.title}
        errorMessage="Use 1–120 characters for the list title."
        onChange={(title) => onChange({ ...value, title })}
      />
      {value.rows.map((item, index) => (
        <fieldset key={index}>
          <legend>Row {index + 1}</legend>
          <TextField
            label={`Row ${index + 1} label`}
            value={item.label ?? ""}
            optional
            schema={rowSchema.shape.label}
            errorMessage="Use at most 120 characters for the label."
            onChange={(label) =>
              row(index, { ...item, label: label || undefined })
            }
          />
          <FieldSelect
            source={item.source}
            kind="all"
            onChange={(field) =>
              row(index, {
                ...item,
                source: { ...item.source, field },
                words: undefined,
              })
            }
          />
          <WordsEditor
            source={item.source}
            words={item.words}
            onChange={(words) => row(index, { ...item, words })}
          />
          <Button
            variant="outline"
            size="touch"
            type="button"
            disabled={index === 0}
            onClick={() => {
              const rows = [...value.rows];
              [rows[index - 1], rows[index]] = [rows[index], rows[index - 1]];
              onChange({ ...value, rows });
            }}
          >
            Move row {index + 1} up
          </Button>
          <Button
            variant="outline"
            size="touch"
            type="button"
            disabled={value.rows.length === 1}
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
          disabled={value.rows.length >= MAX_LIST_ROWS}
          onChange={(event) => {
            const choice = choices.find(
              (candidate) => candidate.id === event.target.value
            );
            if (!choice) {
              return;
            }
            const [field] = Object.keys(stateFields[choice.type] ?? {});
            const source = readingBindingSchema.parse({
              kind: "entity-state",
              entity_id: choice.id,
              entity_type: choice.type,
              field,
            });
            onChange({ ...value, rows: [...value.rows, { source }] });
          }}
        >
          <option value="">Choose an Entity…</option>
          {choices
            .filter((choice) => stateFields[choice.type])
            .map((choice) => (
              <option key={choice.id} value={choice.id}>
                {choice.name}
              </option>
            ))}
        </NativeSelect>
      </EditorLabel>
    </>
  );
};
