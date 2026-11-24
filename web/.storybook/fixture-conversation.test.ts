import { describe, expect, it, vi } from "vitest";

import { projectHistory } from "../src/features/conversation/runtime";
import { conversationHistory, fixtureTurn } from "./application-fixtures";
import { createFixtureConversation } from "./fixture-conversation";

const prompt = "What is the living room temperature?";

describe(createFixtureConversation, () => {
  it("projects seeded history the way a loaded conversation does", () => {
    const history = conversationHistory();
    const conversation = createFixtureConversation({ history });
    expect(conversation.getSnapshot().items).toStrictEqual(
      projectHistory(history)
    );
    expect(conversation.getSnapshot().busy).toBeFalsy();
  });

  it("shows a sent prompt and starts an empty live turn", () => {
    const conversation = createFixtureConversation();
    const listener = vi.fn<() => void>();
    conversation.subscribe(listener);
    conversation.send(`  ${prompt} `);
    const state = conversation.getSnapshot();
    expect(listener).toHaveBeenCalledOnce();
    expect(state.busy).toBeTruthy();
    expect(state.optimistic).toMatchObject({ role: "user", text: prompt });
    expect(state.live).toMatchObject({ reply: "", activities: [] });
  });

  it("reduces turn events into the live turn", () => {
    const conversation = createFixtureConversation();
    conversation.send(prompt);
    const turn = fixtureTurn("reply", [], { text: prompt });
    conversation.apply(turn.events);
    const [started] = conversation.getSnapshot().live?.activities ?? [];
    expect(started?.name).toBe("get_entity");
    expect(started?.result).toBeUndefined();
    conversation.apply(turn.outcome?.results ?? []);
    const { live } = conversation.getSnapshot();
    expect(live?.activities[0]?.result).toBe('{"value":21.5}');
    expect(live?.terminal).toBeTruthy();
  });

  it("stores history before it ends the turn, as the session does", () => {
    const conversation = createFixtureConversation();
    conversation.send(prompt);
    const turn = fixtureTurn("reply", [], { text: prompt });
    const seen: { items: number; busy: boolean }[] = [];
    conversation.subscribe(() => {
      const { items, busy } = conversation.getSnapshot();
      seen.push({ items: items.length, busy });
    });
    conversation.commit(turn.outcome?.history ?? []);
    const stored = projectHistory(turn.outcome?.history ?? []).length;
    expect(seen).toStrictEqual([
      { items: stored, busy: true },
      { items: stored, busy: false },
    ]);
    expect(conversation.getSnapshot()).toMatchObject({
      live: null,
      optimistic: null,
    });
  });

  it("refuses calls the session cannot reach", () => {
    const idle = createFixtureConversation();
    expect(() => idle.apply([])).toThrow("Apply turn events after send");
    expect(() => idle.commit([])).toThrow("Commit history after send");
    idle.send(prompt);
    expect(() => idle.send(prompt)).toThrow(/does not send while busy/u);
    const recovering = createFixtureConversation({ needsRecovery: true });
    expect(() => recovering.send(prompt)).toThrow(/does not send/u);
  });
});
