import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { ConversationHistory } from "./ConversationHistory";
import { ConversationsProvider } from "./ConversationsProvider";

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

const hoursAgo = (hours: number) => new Date(Date.now() - hours * 3600e3).toISOString();
const conversation = (id: string, preview: string, hours: number) => ({ id, created_at: hoursAgo(hours), last_message_at: hoursAgo(hours), message_count: 2, preview });

function renderHistory(conversations: unknown[]) {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    if (String(input) !== "/v1/agent/conversations") throw new Error(`Unexpected request ${String(input)}`);
    return new Response(JSON.stringify({ conversations }));
  }));
  render(<MemoryRouter><ConversationsProvider><ConversationHistory id="history" open={false} /></ConversationsProvider></MemoryRouter>);
  return within(screen.getByRole("region", { name: "Recent" }));
}

it("lists conversations under date headings without a search field for a short list", async () => {
  const recent = renderHistory([conversation("c1", "Kitchen lights", 0), conversation("c2", "Garage door", 24 * 10)]);
  expect(await recent.findByRole("link", { name: "Kitchen lights" })).toBeTruthy();
  expect(recent.getAllByRole("heading", { level: 3 }).map(heading => heading.textContent)).toEqual(["Today", "Previous 30 days"]);
  expect(recent.queryByRole("searchbox")).toBeNull();
});

it("filters a long list by preview, says when nothing matches, and clears with Escape", async () => {
  const recent = renderHistory([
    conversation("garage", "Is the garage door open?", 0),
    ...Array.from({ length: 12 }, (_, index) => conversation(`c${index}`, `Temperature check ${index}`, 30 + index)),
  ]);
  const search = await recent.findByRole("searchbox", { name: "Search conversations" });
  fireEvent.change(search, { target: { value: "  GARAGE " } });
  expect(recent.getAllByRole("link").map(link => link.textContent)).toEqual(["Is the garage door open?"]);
  expect(recent.getAllByRole("heading", { level: 3 }).map(heading => heading.textContent)).toEqual(["Today"]);
  fireEvent.change(search, { target: { value: "porch" } });
  expect(recent.getByRole("status").textContent).toBe("No conversations match “porch”.");
  expect(recent.queryAllByRole("link")).toHaveLength(0);
  // Escape clears the search without reaching a drawer's Escape handler.
  const documentEscape = vi.fn();
  document.addEventListener("keydown", documentEscape);
  fireEvent.keyDown(search, { key: "Escape" });
  document.removeEventListener("keydown", documentEscape);
  expect(documentEscape).not.toHaveBeenCalled();
  expect(search).toHaveProperty("value", "");
  expect(recent.getAllByRole("link")).toHaveLength(13);
  fireEvent.change(search, { target: { value: "garage" } });
  fireEvent.click(recent.getByRole("button", { name: "Clear search" }));
  expect(search).toHaveProperty("value", "");
  expect(document.activeElement).toBe(search);
});
