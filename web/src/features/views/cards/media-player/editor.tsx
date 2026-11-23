import { NativeSelect } from "../../../../components/native-select";
import { EditorLabel } from "../../editor/editor-label";
import { TextField } from "../../editor/text-field";
import type { SensorChoice } from "../time-series/editor";
import { mediaPlayerLayouts, mediaPlayerSchema } from "./schema";
import type { MediaPlayerProps } from "./schema";

const layoutWords: Record<(typeof mediaPlayerLayouts)[number], string> = {
  card: "Card",
  tile: "Tile",
};

/** The MediaPlayer card's label, layout, and one hearth.mediaplayer/v1 source. */
export const MediaPlayerEditor = ({
  value,
  choices,
  onChange,
}: {
  value: MediaPlayerProps;
  choices: SensorChoice[];
  onChange: (value: MediaPlayerProps) => void;
}) => {
  const speakers = choices.filter(
    (choice) => choice.type === "hearth.mediaplayer/v1"
  );
  const missing = !speakers.some(
    (choice) => choice.id === value.source.entity_id
  );
  return (
    <>
      <TextField
        label="Card label"
        value={value.label ?? ""}
        optional
        schema={mediaPlayerSchema.shape.label}
        errorMessage="Use at most 120 characters for the card label."
        onChange={(label) => onChange({ ...value, label: label || undefined })}
      />
      <EditorLabel>
        Layout
        <NativeSelect
          size="auto"
          value={value.layout ?? "card"}
          onChange={(event) =>
            onChange({
              ...value,
              layout: event.target.value as MediaPlayerProps["layout"],
            })
          }
        >
          {mediaPlayerLayouts.map((layout) => (
            <option key={layout} value={layout}>
              {layoutWords[layout]}
            </option>
          ))}
        </NativeSelect>
      </EditorLabel>
      <EditorLabel>
        Speaker
        <NativeSelect
          size="auto"
          value={value.source.entity_id}
          onChange={(event) => {
            const selected = speakers.find(
              (choice) => choice.id === event.target.value
            );
            if (selected) {
              onChange({
                ...value,
                source: {
                  kind: "entity-state",
                  entity_id: selected.id,
                  entity_type: "hearth.mediaplayer/v1",
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
          {speakers.map((choice) => (
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
