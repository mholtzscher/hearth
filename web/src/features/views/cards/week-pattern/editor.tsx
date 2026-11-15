import type { z } from "zod";

import { NativeSelect } from "../../../../components/native-select";
import { stateFields } from "../../bindings/state-fields";
import { EditorLabel } from "../../editor/editor-label";
import { TextField } from "../../editor/text-field";
import { WordsEditor } from "../state/editor";
import { weekPatternSchema } from "./schema";

export const WeekPatternEditor = ({
  value,
  onChange,
}: {
  value: z.infer<typeof weekPatternSchema>;
  onChange: (value: z.infer<typeof weekPatternSchema>) => void;
}) => {
  const fields = Object.entries(
    stateFields[value.source.entity_type] ?? {}
  ).filter(([, field]) => field.value_type === "boolean");
  return (
    <>
      {fields.length > 1 && (
        <EditorLabel>
          State field
          <NativeSelect
            size="auto"
            value={value.source.field ?? ""}
            onChange={(event) =>
              onChange({
                ...value,
                source: { ...value.source, field: event.target.value },
                words: undefined,
              })
            }
          >
            {fields.map(([path]) => (
              <option key={path} value={path}>
                {path || "State root"}
              </option>
            ))}
          </NativeSelect>
        </EditorLabel>
      )}
      <TextField
        label="Label"
        value={value.label ?? ""}
        optional
        schema={weekPatternSchema.shape.label}
        errorMessage="Use at most 120 characters for the label."
        onChange={(label) => onChange({ ...value, label: label || undefined })}
      />
      <WordsEditor
        source={value.source}
        words={value.words}
        onChange={(words) => onChange({ ...value, words })}
      />
    </>
  );
};
