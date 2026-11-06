import { afterEach, describe, expect, it, vi } from "vitest";
import { CommandError, executeCommand, type CommandFailure } from "./commands";

const problem = (status: number, body: Record<string, unknown> = {}) =>
  new Response(JSON.stringify({ title: "Problem", status, detail: "detail", ...body }), { status, headers: { "content-type": "application/problem+json" } });

async function failureOf(response: () => Promise<Response>): Promise<CommandFailure> {
  vi.stubGlobal("fetch", response);
  try {
    await executeCommand("ent_one", { operation: "set", parameters: { value: true } });
  } catch (error) {
    expect(error).toBeInstanceOf(CommandError);
    return (error as CommandError).failure;
  }
  throw new Error("expected a failure");
}

describe("executeCommand", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("posts the operation and parameters and returns the satisfied outcome", async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify({ command_id: "cmd_1", status: "satisfied", observation_id: "obs_1", value: true })));
    vi.stubGlobal("fetch", fetcher);
    await expect(executeCommand("ent_one", { operation: "set", parameters: { value: true } }))
      .resolves.toEqual({ command_id: "cmd_1", status: "satisfied", observation_id: "obs_1", value: true });
    expect(fetcher).toHaveBeenCalledWith("/v1/entities/ent_one/commands", expect.objectContaining({ method: "POST", body: '{"operation":"set","parameters":{"value":true}}' }));
  });

  it.each([
    [409, { code: "entity_disabled", command_id: "cmd_9" }, { kind: "disabled", commandId: "cmd_9" }],
    [503, {}, { kind: "unreachable" }],
    [502, {}, { kind: "rejected" }],
    [504, {}, { kind: "unconfirmed" }],
    [400, {}, { kind: "invalid" }],
    [404, {}, { kind: "not-found" }],
    [500, {}, { kind: "internal" }],
    [413, {}, { kind: "internal" }],
  ])("maps status %i to its failure", async (status, body, expected) => {
    expect(await failureOf(async () => problem(status, body))).toEqual(expected);
  });

  it("reports a request that never completed as a network failure", async () => {
    expect(await failureOf(async () => { throw new TypeError("Failed to fetch"); })).toEqual({ kind: "network" });
  });

  it("rejects an empty success body as an internal failure", async () => {
    expect(await failureOf(async () => new Response(null, { status: 200 }))).toEqual({ kind: "internal" });
  });

  it("rejects a malformed success body as an internal failure", async () => {
    expect(await failureOf(async () => new Response(JSON.stringify({ command_id: "cmd_1", status: "satisfied" })))).toEqual({ kind: "internal" });
  });
});
