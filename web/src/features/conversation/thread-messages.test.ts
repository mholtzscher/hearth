import { describe, expect, it } from "vitest";

import type { ConversationItem } from "./runtime";
import { convertMessage } from "./thread-messages";

const reply = (id: string): ConversationItem => ({
  id,
  role: "assistant",
  text: "",
  activities: [
    {
      id: "call",
      key: "call",
      name: "list_entities",
      arguments: "{}",
      result: undefined,
    },
  ],
  artifacts: [],
});

describe(convertMessage, () => {
  it("shows the followed reply as running while its turn runs", () => {
    expect(convertMessage(reply("7"), "m7", true, "7").status).toStrictEqual({
      type: "running",
    });
  });

  it("shows the same reply as incomplete once nothing follows it", () => {
    expect(convertMessage(reply("7"), "m7", false, "7").status).toStrictEqual({
      type: "incomplete",
      reason: "other",
    });
    expect(convertMessage(reply("6"), "m6", true, "7").status).toStrictEqual({
      type: "incomplete",
      reason: "other",
    });
  });
});
