import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ConversationHistory } from "./conversation-history";
import { ConversationsProvider } from "./conversations-provider";

const hoursAgo = (hours: number) =>
  new Date(Date.now() - hours * 3600e3).toISOString();
const conversation = (id: string, preview: string, hours: number) => ({
  id,
  created_at: hoursAgo(hours),
  last_message_at: hoursAgo(hours),
  message_count: 2,
  preview,
});
const longConversations = () => [
  conversation("garage", "Is the garage door open?", 0),
  ...Array.from({ length: 12 }, (_, index) =>
    conversation(`c${index}`, `Temperature check ${index}`, 30 + index)
  ),
];

const renderHistory = (conversations: unknown[]) => {
  vi.stubGlobal(
    "fetch",
    vi.fn<typeof fetch>((input) => {
      if (String(input) !== "/v1/agent/conversations") {
        throw new Error(`Unexpected request ${String(input)}`);
      }
      return Promise.resolve(Response.json({ conversations }));
    })
  );
  render(
    <MemoryRouter>
      <ConversationsProvider>
        <ConversationHistory id="history" open={false} />
      </ConversationsProvider>
    </MemoryRouter>
  );
  return within(screen.getByRole("region", { name: "Recent" }));
};

describe(ConversationHistory, () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("lists conversations under date headings without a search field for a short list", async () => {
    const recent = renderHistory([
      conversation("c1", "Kitchen lights", 0),
      conversation("c2", "Garage door", 24 * 10),
    ]);
    await expect(
      recent.findByRole("link", { name: "Kitchen lights" })
    ).resolves.toBeTruthy();
    expect(
      recent
        .getAllByRole("heading", { level: 3 })
        .map((heading) => heading.textContent)
    ).toStrictEqual(["Today", "Previous 30 days"]);
    expect(recent.queryByRole("searchbox")).toBeNull();
  });

  it("filters a long list by preview and says when nothing matches", async () => {
    const recent = renderHistory(longConversations());
    const search = await recent.findByRole("searchbox", {
      name: "Search conversations",
    });
    expect(search.dataset.size).toBe("auto");
    fireEvent.change(search, { target: { value: "  GARAGE " } });
    expect(
      recent.getAllByRole("link").map((link) => link.textContent)
    ).toStrictEqual(["Is the garage door open?"]);
    expect(
      recent
        .getAllByRole("heading", { level: 3 })
        .map((heading) => heading.textContent)
    ).toStrictEqual(["Today"]);
    fireEvent.change(search, { target: { value: "porch" } });
    expect(recent.getByRole("status").textContent).toBe(
      "No conversations match “porch”."
    );
    expect(recent.queryAllByRole("link")).toHaveLength(0);
  });

  it("clears the search with Escape without reaching a drawer's Escape handler", async () => {
    const recent = renderHistory(longConversations());
    const search = await recent.findByRole("searchbox", {
      name: "Search conversations",
    });
    fireEvent.change(search, { target: { value: "  GARAGE " } });
    fireEvent.change(search, { target: { value: "porch" } });
    // Escape clears the search without reaching a drawer's Escape handler.
    const documentEscape = vi.fn<() => void>();
    document.addEventListener("keydown", documentEscape);
    fireEvent.keyDown(search, { key: "Escape" });
    document.removeEventListener("keydown", documentEscape);
    expect(documentEscape).not.toHaveBeenCalled();
    expect(search).toHaveProperty("value", "");
    expect(recent.getAllByRole("link")).toHaveLength(13);
  });

  it("focuses the search after clearing it with the clear button", async () => {
    const recent = renderHistory(longConversations());
    const search = await recent.findByRole("searchbox", {
      name: "Search conversations",
    });
    fireEvent.change(search, { target: { value: "  GARAGE " } });
    fireEvent.change(search, { target: { value: "porch" } });
    fireEvent.keyDown(search, { key: "Escape" });
    fireEvent.change(search, { target: { value: "garage" } });
    const clear = recent.getByRole("button", { name: "Clear search" });
    expect(clear.dataset.size).toBe("icon-touch");
    fireEvent.click(clear);
    expect(search).toHaveProperty("value", "");
    expect(document.activeElement).toBe(search);
  });
});
