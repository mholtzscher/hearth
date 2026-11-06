import { pointer, textField } from "./state-fields";
import type { FieldEntity } from "./entity";
import type { EntitySource, TextData, Words } from "./types";

/**
 * Readable names for catalogued choices. Adapter-supplied choice text, such as
 * an enum setting's options, is shown as reported.
 */
const choiceNames: Readonly<Record<string, Readonly<Record<string, string>>>> = {
  "hearth.colormode/v1": { xy: "XY color", hs: "Hue and saturation", color_temp: "Color temperature" },
  "hearth.numericsetting/v1/mode": { value: "Value", choice: "Choice" },
};

/** A boolean's words: the card's own, then the field's convention. */
function defaultWords(source: EntitySource): Words {
  return source.field === "/active" ? { true: "Active", false: "Inactive" } : { true: "On", false: "Off" };
}

/** The words for one State value, or undefined when the field is absent. */
export function stateWords(value: unknown, source: EntitySource, words?: Words): string | undefined {
  const field = textField(source);
  if (!field) throw new Error("Unsupported text field");
  if (value === undefined) return undefined;
  if (field.value_type === "boolean") {
    if (typeof value !== "boolean") throw new Error("Invalid boolean State");
    const chosen = words ?? defaultWords(source);
    return value ? chosen.true : chosen.false;
  }
  if (typeof value !== "string") throw new Error("Invalid text State");
  return choiceNames[source.entity_type + (source.field ?? "")]?.[value] ?? value;
}

/** A yes/no or text reading, null when never observed, or absent when the field isn't in State. */
export function textReading(data: FieldEntity, source: EntitySource, words?: Words): TextData | { absent: true; observed_at: string } | null {
  if (!data.state) return null;
  const text = stateWords(pointer(data.state.value, source.field ?? ""), source, words);
  return text === undefined ? { absent: true, observed_at: data.state.observed_at } : { text, observed_at: data.state.observed_at };
}
