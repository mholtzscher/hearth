import { useId, type ChangeEvent } from "react";
import type { z } from "zod";

/** Validate text with its catalog schema while retaining intermediate edits. */
export function TextField({ label, value, onChange, schema, errorMessage, placeholder, multiline = false, optional = false }: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  schema: z.ZodType<string | undefined>;
  errorMessage: string;
  placeholder?: string;
  multiline?: boolean;
  optional?: boolean;
}) {
  const id = useId();
  const invalid = !schema.safeParse(optional && value === "" ? undefined : value).success;
  const input = {
    id, value, placeholder,
    onChange: (event: ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => onChange(event.target.value),
    "aria-invalid": invalid || undefined,
    "aria-describedby": invalid ? `${id}-error` : undefined,
  };
  return <div className="view-editor-field">
    <label htmlFor={id}>{label}</label>
    {multiline ? <textarea {...input} /> : <input {...input} />}
    {invalid && <p className="view-field-error" id={`${id}-error`}>{errorMessage}</p>}
  </div>;
}
