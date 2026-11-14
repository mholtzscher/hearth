import { describe, expect, it, vi } from "vitest";
import { CommandError, type CommandFailure } from "../../../api/commands";
import type { CommandResult } from "../../../api/types";
import { CommandQueue, type ControlTarget } from "./queue";

const power: ControlTarget = { entityId: "ent_power", operation: "set" };
const satisfied = (value: unknown): CommandResult => ({ command_id: "cmd", status: "satisfied", observation_id: "obs", value });

/** Commands and State refreshes that settle only when a test says so. */
function fixture() {
  const commands: Array<{ value: unknown; resolve: (result: CommandResult) => void; reject: (error: unknown) => void }> = [];
  const refreshes: Array<() => void> = [];
  const execute = vi.fn((_entityId: string, request: { parameters: Record<string, unknown> }) =>
    new Promise<CommandResult>((resolve, reject) => commands.push({ value: request.parameters.value, resolve, reject })));
  const refreshState = vi.fn(() => new Promise<void>(resolve => refreshes.push(resolve)));
  const queue = new CommandQueue({ execute, refreshState });
  const settle = () => new Promise(resolve => setTimeout(resolve, 0));
  return { queue, commands, refreshes, execute, refreshState, settle };
}

describe("CommandQueue", () => {
  it("preserves structured color parameters, including a newer queued color", async () => {
    const { queue, commands, execute, settle } = fixture();
    const color: ControlTarget = { entityId: "ent_color", operation: "set" };
    queue.request(color, { red: 128, green: 32, blue: 16 });
    queue.request(color, { red: 0, green: 0, blue: 0 });
    expect(execute).toHaveBeenNthCalledWith(1, "ent_color", { operation: "set", parameters: { red: 128, green: 32, blue: 16 } });
    commands[0].resolve(satisfied({ red: 128, green: 32, blue: 16 }));
    await settle();
    expect(execute).toHaveBeenNthCalledWith(2, "ent_color", { operation: "set", parameters: { red: 0, green: 0, blue: 0 } });
    expect(queue.snapshot(color)).toEqual({ phase: "pending", requested: { red: 0, green: 0, blue: 0 } });
  });
  it("shows the request as pending, then confirming until the State read lands, then idle", async () => {
    const { queue, commands, refreshes, execute, settle } = fixture();
    const listener = vi.fn();
    queue.subscribe(power, listener);
    queue.request(power, true);
    expect(execute).toHaveBeenCalledWith("ent_power", { operation: "set", parameters: { value: true } });
    expect(queue.snapshot(power)).toEqual({ phase: "pending", requested: true });
    commands[0].resolve(satisfied(true));
    await settle();
    expect(queue.snapshot(power)).toEqual({ phase: "confirming", requested: true });
    refreshes[0]();
    await settle();
    expect(queue.snapshot(power)).toEqual({ phase: "idle" });
    expect(listener).toHaveBeenCalledTimes(3);
  });

  it("sends the first value, then only the latest of the values requested meanwhile", async () => {
    const { queue, commands, execute, settle } = fixture();
    queue.subscribe(power, vi.fn());
    queue.request(power, true);
    queue.request(power, false);
    queue.request(power, true);
    expect(queue.snapshot(power)).toEqual({ phase: "pending", requested: true, queued: { value: true } });
    expect(execute).toHaveBeenCalledTimes(1);
    commands[0].resolve(satisfied(true));
    await settle();
    expect(execute).toHaveBeenCalledTimes(2);
    expect(commands.map(command => command.value)).toEqual([true, true]);
    expect(queue.snapshot(power)).toEqual({ phase: "pending", requested: true });
  });

  it("keeps a failure until the next request and refreshes State to show what happened", async () => {
    const { queue, commands, refreshState, settle } = fixture();
    queue.subscribe(power, vi.fn());
    queue.request(power, true);
    const failure: CommandFailure = { kind: "unconfirmed" };
    commands[0].reject(new CommandError(failure));
    await settle();
    expect(queue.snapshot(power)).toEqual({ phase: "failed", requested: true, failure });
    expect(refreshState).toHaveBeenCalledTimes(1);
    queue.request(power, true);
    expect(queue.snapshot(power)).toEqual({ phase: "pending", requested: true });
  });

  it("drops a failure that a queued request supersedes", async () => {
    const { queue, commands, settle } = fixture();
    queue.subscribe(power, vi.fn());
    queue.request(power, true);
    queue.request(power, false);
    commands[0].reject(new CommandError({ kind: "rejected" }));
    await settle();
    expect(queue.snapshot(power)).toEqual({ phase: "pending", requested: false });
  });

  it("treats an unexpected error as an internal failure", async () => {
    const { queue, commands, settle } = fixture();
    queue.subscribe(power, vi.fn());
    queue.request(power, true);
    commands[0].reject(new Error("bug"));
    await settle();
    expect(queue.snapshot(power)).toMatchObject({ phase: "failed", failure: { kind: "internal" } });
  });

  it("keeps an in-flight Command's state for a card that remounts", async () => {
    const { queue, commands, settle } = fixture();
    const stop = queue.subscribe(power, vi.fn());
    queue.request(power, true);
    stop();
    expect(queue.snapshot(power)).toMatchObject({ phase: "pending" });
    const listener = vi.fn();
    queue.subscribe(power, listener);
    commands[0].resolve(satisfied(true));
    await settle();
    expect(listener).toHaveBeenCalled();
  });

  it("keeps a failure that lands after its last card unmounted", async () => {
    const { queue, commands, settle } = fixture();
    const stop = queue.subscribe(power, vi.fn());
    queue.request(power, true);
    stop();
    commands[0].reject(new CommandError({ kind: "rejected" }));
    await settle();
    queue.subscribe(power, vi.fn());
    expect(queue.snapshot(power)).toMatchObject({ phase: "failed", failure: { kind: "rejected" } });
  });

  it("keeps targets independent", () => {
    const { queue, execute } = fixture();
    const brightness: ControlTarget = { entityId: "ent_brightness", operation: "set" };
    const mode: ControlTarget = { entityId: "ent_mode", operation: "set" };
    queue.request(power, true);
    queue.request(brightness, 40);
    queue.request(mode, "auto");
    expect(execute).toHaveBeenCalledTimes(3);
    expect(execute).toHaveBeenCalledWith("ent_mode", { operation: "set", parameters: { value: "auto" } });
    expect(queue.snapshot(mode)).toEqual({ phase: "pending", requested: "auto" });
    expect(queue.snapshot(brightness)).toEqual({ phase: "pending", requested: 40 });
  });

  it("replace sends a newer value at once and shows only the latest Command's outcome", async () => {
    const { queue, commands, refreshes, execute, settle } = fixture();
    const cover: ControlTarget = { entityId: "ent_cover", operation: "set" };
    queue.subscribe(cover, vi.fn());
    queue.request(cover, { position: 100 }, "replace");
    queue.request(cover, { position: 0 }, "replace");
    expect(execute).toHaveBeenCalledTimes(2);
    expect(queue.snapshot(cover)).toEqual({ phase: "pending", requested: { position: 0 } });
    commands[0].reject(new CommandError({ kind: "unconfirmed" }));
    await settle();
    expect(queue.snapshot(cover)).toEqual({ phase: "pending", requested: { position: 0 } });
    commands[1].resolve(satisfied({ position: 0 }));
    await settle();
    refreshes[0]();
    await settle();
    expect(queue.snapshot(cover)).toEqual({ phase: "idle" });
  });

  it("forget returns to idle and ignores the outcome of the Command in flight", async () => {
    const { queue, commands, settle } = fixture();
    const cover: ControlTarget = { entityId: "ent_cover", operation: "set" };
    const listener = vi.fn();
    queue.subscribe(cover, listener);
    queue.request(cover, { position: 100 }, "replace");
    queue.forget(cover);
    expect(queue.snapshot(cover)).toEqual({ phase: "idle" });
    listener.mockClear();
    commands[0].reject(new CommandError({ kind: "unconfirmed" }));
    await settle();
    expect(queue.snapshot(cover)).toEqual({ phase: "idle" });
    expect(listener).not.toHaveBeenCalled();
  });

  it("reports dispatched Stop without waiting for a State refresh", async () => {
    const { queue, commands, refreshState, settle } = fixture();
    const stop: ControlTarget = { entityId: "ent_cover", operation: "stop" };
    queue.subscribe(stop, vi.fn());
    queue.request(stop, {}, "replace");
    commands[0].resolve({ command_id: "cmd", status: "dispatched" });
    await settle();
    expect(refreshState).toHaveBeenCalledTimes(1);
    expect(queue.snapshot(stop)).toMatchObject({ phase: "dispatched", requested: {} });
  });

  // React StrictMode disposes the provider's queue on mount and keeps using the
  // same instance, so disposal must not end the queue.
  it("ignores outcomes from before disposal and stays usable afterward", async () => {
    const { queue, commands, execute, settle } = fixture();
    const listener = vi.fn();
    queue.subscribe(power, listener);
    queue.request(power, true);
    listener.mockClear();
    queue.dispose();
    commands[0].resolve(satisfied(true));
    await settle();
    expect(listener).not.toHaveBeenCalled();
    expect(queue.snapshot(power)).toEqual({ phase: "idle" });
    queue.request(power, false);
    expect(execute).toHaveBeenCalledTimes(2);
    expect(queue.snapshot(power)).toEqual({ phase: "pending", requested: false });
    // The card subscribed before disposal still hears about the new request.
    expect(listener).toHaveBeenCalled();
  });
});
