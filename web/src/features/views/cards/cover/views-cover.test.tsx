import { StrictMode, useState, type ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { ViewDataProvider } from "../../data/provider";
import type { FieldEntity } from "../../bindings/entity";
import { BoundCover } from "./Cover";
import type { CoverProps } from "./schema";

const id = "ent_019a3e6a-4111-7000-8000-000000000401";
const source = { kind: "entity-state" as const, entity_id: id, entity_type: "hearth.cover/v1" as const };
type Motion = "idle" | "opening" | "closing";
function cover(position: number, motion: Motion = "idle", operations: object = { set: {}, stop: {} }): FieldEntity {
  return {
    id, name: "Garage door", type: "hearth.cover/v1", enabled: true, availability: { status: "available" },
    support: { state: {}, operations }, quantities: { "/position": { unit: "%", label: "%", fraction_digits: 0, range: { minimum: 0, maximum: 100 } } },
    state: { value: { position, motion }, observed_at: "2026-11-09T10:00:00Z" },
  };
}
type Post = { operation: string; parameters: Record<string, unknown>; respond: (response: Response) => void };
function core(value: FieldEntity) {
  const posts: Post[] = [];
  let reads = 0;
  const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    if (String(input).endsWith("/commands")) {
      const body = JSON.parse(String(init?.body));
      return new Promise<Response>(respond => posts.push({ ...body, respond }));
    }
    reads++;
    return response(api.value);
  });
  const api = {
    value, posts, reads: () => reads,
    report(position: number, motion: Motion = "idle") { api.value = { ...api.value, state: { value: { position, motion }, observed_at: new Date().toISOString() } }; },
    satisfy(post: Post) { post.respond(response({ command_id: "cmd", status: "satisfied", observation_id: "obs", value: post.parameters })); },
    dispatch(post: Post) { post.respond(response({ command_id: "cmd", status: "dispatched" })); },
    fail(post: Post, status: number) { post.respond(response({ status, detail: "failed" }, status)); },
  };
  return api;
}
const flush = () => act(() => vi.advanceTimersByTimeAsync(0));
const wait = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));
async function mount(children: ReactNode) { render(<StrictMode><ViewDataProvider>{children}</ViewDataProvider></StrictMode>, { wrapper: MemoryRouter }); await flush(); }
const card: CoverProps = { layout: "card", label: "Garage door", source };
const tile: CoverProps = { layout: "tile", label: "Garage door", source };
const status = () => document.querySelector(".view-cover-status")?.textContent ?? "";
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date("2026-11-09T10:00:30Z")); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.useRealTimers(); });

it("words the reported State and its position", async () => {
  for (const [position, motion, word] of [[0, "idle", "Closed"], [100, "idle", "Open"], [40, "idle", "Partly open"], [40, "opening", "Opening"], [40, "closing", "Closing"]] as const) {
    core(cover(position, motion)); await mount(<BoundCover {...card} />);
    expect(document.querySelector(".view-metric-value")?.textContent).toBe(word);
    expect(document.querySelector(".view-cover-position")?.textContent).toBe(`${position} % open`);
    cleanup(); vi.restoreAllMocks();
  }
});

it("asks for a second press before opening, and the first press expires", async () => {
  const api = core(cover(0)); await mount(<BoundCover {...card} />);
  fireEvent.click(screen.getByRole("button", { name: "Open Garage door" }));
  expect(api.posts).toHaveLength(0);
  expect(screen.getByRole("button", { name: "Confirm open Garage door" }).textContent).toBe("Confirm");
  expect(status()).toBe("Press Confirm within 4 s to open.");
  await wait(4_000);
  expect(screen.getByRole("button", { name: "Open Garage door" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Open Garage door" }));
  fireEvent.click(screen.getByRole("button", { name: "Confirm open Garage door" })); await flush();
  expect(api.posts).toEqual([expect.objectContaining({ operation: "set", parameters: { position: 100 } })]);
  expect(status()).toBe("Opening…");
  expect(screen.getByText("Closed")).toBeTruthy();
});

it("closes in one press, and opens in one press when confirm_open is false", async () => {
  const api = core(cover(40)); await mount(<BoundCover {...card} confirm_open={false} />);
  fireEvent.click(screen.getByRole("button", { name: "Close Garage door" })); await flush();
  fireEvent.click(screen.getByRole("button", { name: "Open Garage door" })); await flush();
  expect(api.posts.map(post => post.parameters)).toEqual([{ position: 0 }, { position: 100 }]);
});

it("keeps Open and Close enabled at either end, because a reported position can be wrong", async () => {
  core(cover(0)); await mount(<BoundCover {...card} />);
  expect((screen.getByRole("button", { name: "Close Garage door" }) as HTMLButtonElement).disabled).toBe(false);
  expect((screen.getByRole("button", { name: "Open Garage door" }) as HTMLButtonElement).disabled).toBe(false);
});

it("still confirms Open at the reported 100 % endpoint", async () => {
  const api = core(cover(100)); await mount(<BoundCover {...card} />);
  fireEvent.click(screen.getByRole("button", { name: "Open Garage door" }));
  expect(api.posts).toHaveLength(0);
  fireEvent.click(screen.getByRole("button", { name: "Confirm open Garage door" })); await flush();
  expect(api.posts[0].parameters).toEqual({ position: 100 });
});

it("closes an unobserved cover in one press", async () => {
  const api = core({ ...cover(0), state: null }); await mount(<BoundCover {...card} />);
  fireEvent.click(screen.getByRole("button", { name: "Close Garage door" })); await flush();
  expect(api.posts[0].parameters).toEqual({ position: 0 });
});

it("does not transfer an armed confirmation when the source changes", async () => {
  const api = core(cover(0));
  const secondId = `${id.slice(0, -1)}2`;
  function EditorHarness() {
    const [props, setProps] = useState(card);
    return <><BoundCover {...props} /><button onClick={() => setProps({ ...card, source: { ...source, entity_id: secondId } })}>Change source</button></>;
  }
  await mount(<EditorHarness />);
  fireEvent.click(screen.getByRole("button", { name: "Open Garage door" }));
  api.value = { ...api.value, id: secondId };
  fireEvent.click(screen.getByText("Change source")); await flush();
  expect(screen.queryByRole("button", { name: "Confirm open Garage door" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Open Garage door" }));
  expect(api.posts).toHaveLength(0);
  expect(screen.getByRole("button", { name: "Confirm open Garage door" })).toBeTruthy();
});

it("sends a newer position at once instead of waiting for the earlier Command", async () => {
  const api = core(cover(40)); await mount(<BoundCover {...card} confirm_open={false} />);
  fireEvent.click(screen.getByRole("button", { name: "Open Garage door" })); await flush();
  fireEvent.click(screen.getByRole("button", { name: "Close Garage door" })); await flush();
  expect(api.posts.map(post => post.parameters)).toEqual([{ position: 100 }, { position: 0 }]);
  api.fail(api.posts[0], 504); await flush();
  expect(status()).toBe("Closing…");
});

it("sends Stop at once, hides the earlier position Command's outcome, and says Stop was sent", async () => {
  const api = core(cover(40, "closing")); await mount(<BoundCover {...card} />);
  fireEvent.click(screen.getByRole("button", { name: "Close Garage door" })); await flush();
  fireEvent.click(screen.getByRole("button", { name: "Stop Garage door" })); await flush();
  expect(api.posts.map(post => post.operation)).toEqual(["set", "stop"]);
  expect(api.posts[1].parameters).toEqual({});
  expect(status()).toBe("Sending Stop…");
  api.report(38); api.dispatch(api.posts[1]); await flush(); await flush();
  expect(status()).toBe("Stop sent.");
  expect(screen.getByText("Partly open")).toBeTruthy();
  api.fail(api.posts[0], 504); await flush();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(status()).toBe("Stop sent.");
});

it("warns when the cover is still moving after Stop was sent", async () => {
  const api = core(cover(60, "closing")); await mount(<BoundCover {...card} />);
  fireEvent.click(screen.getByRole("button", { name: "Stop Garage door" })); await flush();
  api.dispatch(api.posts[0]); await flush(); await flush();
  expect(status()).toBe("Stop sent.");
  await wait(3_000);
  expect(status()).toBe("Stop sent, but the cover is still closing.");
});

it("sends a second Stop immediately around a newer position request", async () => {
  const api = core(cover(40, "closing")); await mount(<BoundCover {...card} confirm_open={false} />);
  fireEvent.click(screen.getByRole("button", { name: "Stop Garage door" })); await flush();
  fireEvent.click(screen.getByRole("button", { name: "Open Garage door" })); await flush();
  fireEvent.click(screen.getByRole("button", { name: "Stop Garage door" })); await flush();
  expect(api.posts.map(post => post.operation)).toEqual(["stop", "set", "stop"]);
  api.fail(api.posts[0], 502); await flush();
  expect(status()).toBe("Sending Stop…");
  api.dispatch(api.posts[2]); await flush();
  expect(status()).toBe("Stop sent.");
});

it("supersedes an old Stop failure when a position request succeeds", async () => {
  const api = core(cover(40)); await mount(<BoundCover {...card} />);
  fireEvent.click(screen.getByRole("button", { name: "Stop Garage door" })); await flush();
  api.fail(api.posts[0], 502); await flush();
  expect(screen.getByRole("alert")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Close Garage door" })); await flush();
  api.report(0); api.satisfy(api.posts[1]); await flush(); await flush();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(status()).toBe("");
});

it("names a failed position Command until the next request", async () => {
  const api = core(cover(40)); await mount(<BoundCover {...card} />);
  fireEvent.click(screen.getByRole("button", { name: "Close Garage door" })); await flush();
  api.fail(api.posts[0], 502); await flush();
  expect(screen.getByRole("alert").textContent).toBe("Change rejected. Try again.");
  fireEvent.click(screen.getByRole("button", { name: "Close Garage door" })); await flush();
  expect(screen.queryByRole("alert")).toBeNull();
});

it("re-reads State every second while the cover moves, and every ten seconds once it stops", async () => {
  const api = core(cover(20, "opening")); await mount(<BoundCover {...card} />);
  const start = api.reads();
  await wait(3_000);
  expect(api.reads() - start).toBe(3);
  api.report(100); await wait(1_000);
  const idle = api.reads();
  await wait(5_000);
  expect(api.reads()).toBe(idle);
});

it("hides Stop without stop support and offers no controls without set support", async () => {
  core(cover(40, "idle", { set: {} })); await mount(<BoundCover {...card} />);
  expect(screen.queryByRole("button", { name: "Stop Garage door" })).toBeNull();
  cleanup(); vi.restoreAllMocks();
  core(cover(40, "idle", {})); await mount(<BoundCover {...card} />);
  expect(screen.queryByRole("button", { name: /Garage door$/ })).toBeNull();
});

it("offers Stop when that is the cover's only writable Operation", async () => {
  const api = core(cover(40, "closing", { stop: {} })); await mount(<BoundCover {...card} />);
  expect(screen.queryByRole("button", { name: "Open Garage door" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Stop Garage door" })); await flush();
  expect(api.posts[0].operation).toBe("stop");
  api.fail(api.posts[0], 504); await flush();
  expect(screen.getByRole("alert").textContent).toBe("Stop wasn't confirmed as sent. It may still apply.");
});

it("tile: opens a closed cover after a second press, then stops it while it moves", async () => {
  const api = core(cover(0)); await mount(<BoundCover {...tile} />);
  const press = () => fireEvent.click(document.querySelector(".view-toggle-tile-action")!);
  expect(screen.getByText("Press to open")).toBeTruthy();
  press(); expect(api.posts).toHaveLength(0);
  expect(document.querySelector(".view-cover-short")?.textContent).toBe("Press again.");
  press(); await flush();
  expect(api.posts[0].parameters).toEqual({ position: 100 });
  expect(screen.getByText("Press to stop")).toBeTruthy();
  press(); await flush();
  expect(api.posts[1].operation).toBe("stop");
});

it("tile: closes a partly open cover in one press and keeps one-line rows", async () => {
  const api = core(cover(40)); await mount(<BoundCover {...tile} />);
  expect(document.querySelector(".view-metric-value")?.textContent).toBe("Open");
  expect(document.querySelector(".view-cover-position")?.textContent).toBe("40 % open");
  fireEvent.click(document.querySelector(".view-toggle-tile-action")!); await flush();
  expect(api.posts[0].parameters).toEqual({ position: 0 });
  api.fail(api.posts[0], 502); await flush();
  const alert = screen.getByRole("alert");
  expect(alert.querySelector(".view-cover-short")?.textContent).toBe("Rejected.");
  expect(alert.querySelector(".sr-only")?.textContent).toBe("Change rejected. Try again.");
  expect(document.querySelectorAll(".view-cover-tile .view-cover-row")).toHaveLength(3);
});
