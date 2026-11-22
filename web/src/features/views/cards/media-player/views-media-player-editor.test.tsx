import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { entityId } from "../../../../../.storybook/fixtures";
import { MediaPlayerEditor } from "./editor";
import type { MediaPlayerProps } from "./schema";

const source = (id: number): MediaPlayerProps["source"] => ({
  kind: "entity-state",
  entity_id: entityId(id),
  entity_type: "hearth.mediaplayer/v1",
});
const choices = [
  {
    id: entityId(1),
    name: "Speaker",
    type: "hearth.mediaplayer/v1",
    device_name: "Living room",
  },
  { id: entityId(2), name: "Kitchen", type: "hearth.mediaplayer/v1" },
  { id: entityId(3), name: "Temperature", type: "hearth.temperature/v1" },
];

describe("MediaPlayer editor", () => {
  afterEach(cleanup);

  it("defaults to Card and edits the layout independently of the speaker", () => {
    const onChange = vi.fn<(value: MediaPlayerProps) => void>();
    render(
      <MediaPlayerEditor
        value={{ source: source(1) }}
        choices={choices}
        onChange={onChange}
      />
    );
    const layout = screen.getByLabelText<HTMLSelectElement>("Layout");
    expect(layout.value).toBe("card");
    fireEvent.change(layout, { target: { value: "tile" } });
    expect(onChange).toHaveBeenLastCalledWith({
      source: source(1),
      layout: "tile",
    });
  });

  it("keeps an existing missing source and offers only media players", () => {
    const onChange = vi.fn<(value: MediaPlayerProps) => void>();
    render(
      <MediaPlayerEditor
        value={{ source: source(9), layout: "tile" }}
        choices={choices}
        onChange={onChange}
      />
    );
    const select = screen.getByLabelText<HTMLSelectElement>("Speaker");
    expect(select.value).toBe(entityId(9));
    expect(within(select).getByText("Living room · Speaker")).toBeTruthy();
    expect(within(select).queryByText("Temperature")).toBeNull();
    fireEvent.change(select, { target: { value: entityId(2) } });
    expect(onChange).toHaveBeenLastCalledWith({
      source: source(2),
      layout: "tile",
    });
  });
});
