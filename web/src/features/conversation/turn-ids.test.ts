import { describe, expect, it } from "vitest";

import type { ConversationItem } from "./runtime";
import { inFlightIds } from "./turn-ids";

const item = (
  id: string,
  role: ConversationItem["role"]
): ConversationItem => ({
  id,
  role,
  text: "",
  activities: [],
  artifacts: [],
});

describe(inFlightIds, () => {
  it("names the sent prompt and its reply for their turn", () => {
    const replies = [
      item("1", "user"),
      item("2", "assistant"),
      item("pending-user-7", "user"),
      item("live", "assistant"),
    ];
    expect(inFlightIds("c-1", replies, "pending-user-7")).toStrictEqual([
      { index: 2, role: "user", id: "c-1:pending-user-7" },
      { index: 3, role: "assistant", id: "c-1:reply-to-pending-user-7" },
    ]);
  });

  it("keeps a reopened live reply without a prompt on the live id", () => {
    expect(
      inFlightIds(
        "c-1",
        [item("1", "user"), item("live", "assistant")],
        undefined
      )
    ).toStrictEqual([{ index: 1, role: "assistant", id: "c-1:live" }]);
  });

  it("names nothing once the turn is stored", () => {
    expect(
      inFlightIds("c-1", [item("1", "user"), item("2", "assistant")], undefined)
    ).toStrictEqual([]);
  });
});
