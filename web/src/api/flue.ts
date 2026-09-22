// The Flue household agent is a separate runtime (agent-flue) addressed
// through the dashboard's same-origin `/flue` proxy (vite dev server and the
// nginx image render it). The browser talks to one conversation URL; Flue owns
// durable conversation state, while the caller-chosen instance ID persists in
// localStorage exactly like the built-in agent's conversation ID.

/** localStorage key holding the active Flue agent instance ID. */
export const FLUE_INSTANCE_KEY = "hearth.flueAgentInstanceId";

/** Where agent-flue mounts the household agent's router. */
export const FLUE_AGENT_PATH = "/flue/agents/household";

/** Conversation URL for one caller-chosen instance ID. */
export function flueConversationUrl(instanceId: string): string {
  return `${FLUE_AGENT_PATH}/${encodeURIComponent(instanceId)}`;
}

/** A random, URL-safe instance ID. The `web-` prefix keeps locally minted IDs
 *  distinguishable from IDs created by other clients of the same agent. */
export function newFlueInstanceId(): string {
  const uuid =
    typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
      ? crypto.randomUUID()
      : `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
  return `web-${uuid}`;
}

/** The persisted instance ID, minting and storing one on first use. */
export function loadOrCreateFlueInstanceId(): string {
  const stored = localStorage.getItem(FLUE_INSTANCE_KEY);
  if (stored && stored.trim() !== "") return stored;
  const created = newFlueInstanceId();
  localStorage.setItem(FLUE_INSTANCE_KEY, created);
  return created;
}
