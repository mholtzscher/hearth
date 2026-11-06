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
    queue.request(power, true);
    queue.request(brightness, 40);
    expect(execute).toHaveBeenCalledTimes(2);
    expect(queue.snapshot(brightness)).toEqual({ phase: "pending", requested: 40 });
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
