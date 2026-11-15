import { Input } from "../../../../components/input";
import { NativeSelect } from "../../../../components/native-select";
import { EditorLabel } from "../../editor/editor-label";
import { TextField } from "../../editor/text-field";
import type { SensorChoice } from "../time-series/editor";
import { confirmsOpen, coverSchema } from "./schema";
import type { CoverProps } from "./schema";

export const CoverEditor = ({
  value,
  choices,
  onChange,
}: {
  value: CoverProps;
  choices: SensorChoice[];
  onChange: (value: CoverProps) => void;
}) => {
  const covers = choices.filter((choice) => choice.type === "hearth.cover/v1");
  const missing = !covers.some(
    (choice) => choice.id === value.source.entity_id
  );
  return (
    <>
      <TextField
        label="Card label"
        value={value.label ?? ""}
        optional
        schema={coverSchema.shape.label}
        errorMessage="Use at most 120 characters for the card label."
        onChange={(label) => onChange({ ...value, label: label || undefined })}
      />
      <EditorLabel>
        Layout
        <NativeSelect
          size="auto"
          value={value.layout}
          onChange={(event) =>
            onChange({
              ...value,
              layout: event.target.value as CoverProps["layout"],
            })
          }
        >
          <option value="card">Card</option>
          <option value="tile">Tile</option>
        </NativeSelect>
      </EditorLabel>
      <EditorLabel>
        Cover
        <NativeSelect
          size="auto"
          value={value.source.entity_id}
          onChange={(event) => {
            const selected = covers.find(
              (choice) => choice.id === event.target.value
            );
            if (selected) {
              onChange({
                ...value,
                source: {
                  kind: "entity-state",
                  entity_id: selected.id,
                  entity_type: "hearth.cover/v1",
                },
              });
            }
          }}
        >
          {missing && value.source.entity_id && (
            <option value={value.source.entity_id}>
              Existing source · {value.source.entity_id}
            </option>
          )}
          {covers.map((choice) => (
            <option key={choice.id} value={choice.id}>
              {choice.device_name ? `${choice.device_name} · ` : ""}
              {choice.name}
            </option>
          ))}
        </NativeSelect>
      </EditorLabel>
      <EditorLabel className="flex items-center gap-2">
        <Input
          size="auto"
          className="w-auto"
          type="checkbox"
          checked={confirmsOpen(value)}
          onChange={(event) =>
            onChange({
              ...value,
              confirm_open: event.target.checked ? undefined : false,
            })
          }
        />
        Ask before opening
      </EditorLabel>
    </>
  );
};
