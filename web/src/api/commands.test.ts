import { afterEach, describe, expect, it, vi } from "vitest";

import { CommandError, executeCommand } from "./commands";
import type { CommandFailure } from "./commands";

const problem = (status: number, body: Record<string, unknown> = {}) =>
  Response.json(
    { title: "Problem", status, detail: "detail", ...body },
    { status, headers: { "content-type": "application/problem+json" } }
  );

const failureOf = async (response: () => Response): Promise<CommandFailure> => {
  vi.stubGlobal("fetch", response);
  let error: unknown;
  try {
    await executeCommand("ent_one", {
      operation: "set",
      parameters: { value: true },
    });
  } catch (error_) {
    error = error_;
  }
  if (error === undefined) {
    throw new Error("expected a failure");
  }
  expect(error).toBeInstanceOf(CommandError);
  if (!(error instanceof CommandError)) {
    throw new TypeError("expected a CommandError", { cause: error });
  }
  return error.failure;
};

describe(executeCommand, () => {
  afterEach(() => vi.unstubAllGlobals());

  it("posts the operation and parameters and returns the satisfied outcome", async () => {
    const fetcher = vi.fn<typeof fetch>(() =>
      Promise.resolve(
        Response.json({
          command_id: "cmd_1",
          status: "satisfied",
          observation_id: "obs_1",
          value: true,
        })
      )
    );
    vi.stubGlobal("fetch", fetcher);
    await expect(
      executeCommand("ent_one", {
        operation: "set",
        parameters: { value: true },
      })
    ).resolves.toStrictEqual({
      command_id: "cmd_1",
      status: "satisfied",
      observation_id: "obs_1",
      value: true,
    });
    expect(fetcher).toHaveBeenCalledWith(
      "/v1/entities/ent_one/commands",
      expect.objectContaining({
        method: "POST",
        body: '{"operation":"set","parameters":{"value":true}}',
      })
    );
  });

  it.each([
    [
      409,
      { code: "entity_disabled", command_id: "cmd_9" },
      { kind: "disabled", commandId: "cmd_9" },
    ],
    [503, {}, { kind: "unreachable" }],
    [502, {}, { kind: "rejected" }],
    [504, {}, { kind: "unconfirmed" }],
    [400, {}, { kind: "invalid" }],
    [404, {}, { kind: "not-found" }],
    [500, {}, { kind: "internal" }],
    [413, {}, { kind: "internal" }],
  ])("maps status %i to its failure", async (status, body, expected) => {
    await expect(failureOf(() => problem(status, body))).resolves.toStrictEqual(
      expected
    );
  });

  it("reports a request that never completed as a network failure", async () => {
    await expect(
      failureOf(() => {
        throw new TypeError("Failed to fetch");
      })
    ).resolves.toStrictEqual({ kind: "network" });
  });

  it("rejects an empty success body as an internal failure", async () => {
    await expect(
      failureOf(() => new Response(null, { status: 200 }))
    ).resolves.toStrictEqual({ kind: "internal" });
  });

  it("rejects a malformed success body as an internal failure", async () => {
    await expect(
      failureOf(() =>
        Response.json({ command_id: "cmd_1", status: "satisfied" })
      )
    ).resolves.toStrictEqual({ kind: "internal" });
  });
});
