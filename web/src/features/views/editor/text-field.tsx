import { useId } from "react";
import type { ChangeEvent } from "react";
import type { z } from "zod";

import { Input } from "../../../components/input";
import { Textarea } from "../../../components/textarea";
import { EditorLabel } from "./editor-label";

/** Validate text with its catalog schema while retaining intermediate edits. */
export const TextField = ({
  label,
  value,
  onChange,
  schema,
  errorMessage,
  placeholder,
  multiline = false,
  optional = false,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  schema: z.ZodType<string | undefined>;
  errorMessage: string;
  placeholder?: string;
  multiline?: boolean;
  optional?: boolean;
}) => {
  const id = useId();
  const invalid = !schema.safeParse(
    optional && value === "" ? undefined : value
  ).success;
  const input = {
    id,
    value,
    placeholder,
    onChange: (event: ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
      onChange(event.target.value),
    "aria-invalid": invalid || undefined,
    "aria-describedby": invalid ? `${id}-error` : undefined,
  };
  return (
    <div>
      <EditorLabel htmlFor={id}>{label}</EditorLabel>
      {multiline ? <Textarea {...input} /> : <Input size="auto" {...input} />}
      {invalid && (
        <p className="text-destructive" id={`${id}-error`}>
          {errorMessage}
        </p>
      )}
    </div>
  );
};
