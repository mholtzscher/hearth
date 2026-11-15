import type { z } from "zod";

import { Input } from "../../../../components/input";
import { wordsSchema } from "../../bindings/schema";
import { textField } from "../../bindings/state-fields";
import type { EntitySource, Words } from "../../bindings/types";
import { EditorLabel } from "../../editor/editor-label";
import { FieldSelect } from "../../editor/field-select";
import { TextField } from "../../editor/text-field";
import { stateSchema } from "./schema";

export const WordsEditor = ({
  source,
  words,
  onChange,
}: {
  source: EntitySource;
  words?: Words;
  onChange: (words?: Words) => void;
}) => {
  if (textField(source)?.value_type !== "boolean") {
    return null;
  }
  return (
    <fieldset>
      <legend>Yes/no words</legend>
      <EditorLabel className="flex items-center gap-2">
        <Input
          size="auto"
          className="w-auto"
          type="checkbox"
          checked={!!words}
          onChange={(event) =>
            onChange(
              event.target.checked ? { true: "On", false: "Off" } : undefined
            )
          }
        />
        Use custom words
      </EditorLabel>
      {words && (
        <>
          <TextField
            label="When true"
            value={words.true}
            schema={wordsSchema.shape.true}
            errorMessage="Use 1–40 characters."
            onChange={(text) => onChange({ ...words, true: text })}
          />
          <TextField
            label="When false"
            value={words.false}
            schema={wordsSchema.shape.false}
            errorMessage="Use 1–40 characters."
            onChange={(text) => onChange({ ...words, false: text })}
          />
        </>
      )}
    </fieldset>
  );
};

export const StateEditor = ({
  value,
  onChange,
}: {
  value: z.infer<typeof stateSchema>;
  onChange: (value: z.infer<typeof stateSchema>) => void;
}) => (
  <>
    <FieldSelect
      source={value.source}
      kind="text"
      onChange={(field) =>
        onChange({
          ...value,
          source: { ...value.source, field },
          words: undefined,
        })
      }
    />
    <TextField
      label="Label"
      value={value.label ?? ""}
      optional
      schema={stateSchema.shape.label}
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
