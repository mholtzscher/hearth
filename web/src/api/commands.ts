import { z } from "zod";

import { apiFetch, ApiError } from "./client";
import type { CommandResult } from "./types";

/** Why a Command a control sent did not reach its outcome. Each kind maps from one documented response. */
export type CommandFailure =
  // 409 entity_disabled
  | { kind: "disabled"; commandId?: string }
  // 503: adapter unhealthy, entity unavailable, or admission closed
  | { kind: "unreachable" }
  // 502: the upstream system rejected the Command
  | { kind: "rejected" }
  // 504: no matching observation before the deadline; the change may still apply
  | { kind: "unconfirmed" }
  // 400
  | { kind: "invalid" }
  // 404
  | { kind: "not-found" }
  // the request never completed; the change may still apply
  | { kind: "network" }
  // 500, any other status, or a malformed response
  | { kind: "internal" };

export interface CommandRequest {
  operation: string;
  parameters: Record<string, unknown>;
}

export class CommandError extends Error {
  readonly failure: CommandFailure;
  constructor(commandFailure: CommandFailure) {
    super(`Command failed: ${commandFailure.kind}`);
    this.name = "CommandError";
    this.failure = commandFailure;
  }
}

const resultBody = z.discriminatedUnion("status", [
  z.object({
    command_id: z.string(),
    status: z.literal("satisfied"),
    observation_id: z.string(),
    value: z.unknown(),
  }),
  z.object({ command_id: z.string(), status: z.literal("dispatched") }),
]);

const failure = (error: ApiError): CommandFailure => {
  switch (error.status) {
    case 409: {
      const detail =
        typeof error.detail === "object" ? error.detail : undefined;
      return {
        kind: "disabled",
        commandId:
          typeof detail?.command_id === "string"
            ? detail.command_id
            : undefined,
      };
    }
    case 503: {
      return { kind: "unreachable" };
    }
    case 502: {
      return { kind: "rejected" };
    }
    case 504: {
      return { kind: "unconfirmed" };
    }
    case 400: {
      return { kind: "invalid" };
    }
    case 404: {
      return { kind: "not-found" };
    }
    default: {
      return { kind: "internal" };
    }
  }
};

/**
 * Sends one Command and waits for its outcome, which takes up to the
 * Operation's deadline. Rejects with a CommandError and never retries. It takes
 * no AbortSignal: aborting would stop only the wait, not the Command.
 */
export const executeCommand = async (
  entityId: string,
  request: CommandRequest
): Promise<CommandResult> => {
  try {
    const body = await apiFetch(
      `/v1/entities/${encodeURIComponent(entityId)}/commands`,
      {
        method: "POST",
        body: JSON.stringify(request),
      }
    );
    // apiFetch skips decoding an empty body, so validate the result here.
    return resultBody.parse(body);
  } catch (error) {
    if (error instanceof ApiError) {
      throw new CommandError(failure(error));
    }
    // fetch rejects with a TypeError when the request never completes.
    if (error instanceof TypeError) {
      throw new CommandError({ kind: "network" });
    }
    throw new CommandError({ kind: "internal" });
  }
};
