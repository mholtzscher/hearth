import { NativeSelect } from "../../../../components/native-select";
import { EditorLabel } from "../../editor/editor-label";
import { TextField } from "../../editor/text-field";
import type { SensorChoice } from "../time-series/editor";
import { thermostatSchema } from "./schema";
import type { ThermostatProps } from "./schema";

/** The Thermostat card's label and its one hearth.thermostat/v1 source. */
export const ThermostatEditor = ({
  value,
  choices,
  onChange,
}: {
  value: ThermostatProps;
  choices: SensorChoice[];
  onChange: (value: ThermostatProps) => void;
}) => {
  const thermostats = choices.filter(
    (choice) => choice.type === "hearth.thermostat/v1"
  );
  const missing = !thermostats.some(
    (choice) => choice.id === value.source.entity_id
  );
  return (
    <>
      <TextField
        label="Card label"
        value={value.label ?? ""}
        optional
        schema={thermostatSchema.shape.label}
        errorMessage="Use at most 120 characters for the card label."
        onChange={(label) => onChange({ ...value, label: label || undefined })}
      />
      <EditorLabel>
        Thermostat
        <NativeSelect
          size="auto"
          value={value.source.entity_id}
          onChange={(event) => {
            const selected = thermostats.find(
              (choice) => choice.id === event.target.value
            );
            if (selected) {
              onChange({
                ...value,
                source: {
                  kind: "entity-state",
                  entity_id: selected.id,
                  entity_type: "hearth.thermostat/v1",
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
          {thermostats.map((choice) => (
            <option key={choice.id} value={choice.id}>
              {choice.device_name ? `${choice.device_name} · ` : ""}
              {choice.name}
            </option>
          ))}
        </NativeSelect>
      </EditorLabel>
    </>
  );
};
