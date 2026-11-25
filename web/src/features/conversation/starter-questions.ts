import type { ThreadSuggestion } from "@assistant-ui/react";
import { useCallback, useEffect, useRef, useState } from "react";

/** Shown when Core has no generated starter questions or cannot be read. */
export const FALLBACK_STARTER_QUESTIONS: readonly ThreadSuggestion[] = [
  {
    title: "Show me the temperatures",
    prompt: "Show me the temperatures throughout the house.",
  },
  {
    title: "Which devices are unavailable?",
    prompt: "Which devices are unavailable right now?",
  },
  {
    title: "Chart light brightness today",
    prompt: "Show light brightness over the past day.",
  },
];

interface StarterQuestionSelectionInput {
  /** The thread has no stored, optimistic, live, or queued item. */
  empty: boolean;
  /** Conversation history is still loading. */
  loading: boolean;
  /** Core's generated questions, or null before they load or when the read fails. */
  served: readonly ThreadSuggestion[] | null;
  /** The first read of Core's questions has not answered yet. */
  pending: boolean;
  /** Core generates questions, so they can be regenerated on request. */
  enabled: boolean;
  /** Reads Core's questions again, keeping the current ones on failure. */
  refresh: () => void;
  /** Asks Core to generate new questions now. */
  regenerate: () => Promise<readonly ThreadSuggestion[]>;
}

export interface StarterQuestionSelection {
  questions: readonly ThreadSuggestion[];
  /** Offer "Suggest other questions": questions are showing and Core generates them. */
  canRegenerate: boolean;
  regenerating: boolean;
  /** The last request for new questions failed; the shown questions remain. */
  regenerateFailed: boolean;
  regenerate: () => Promise<void>;
}

const NO_STARTER_QUESTIONS: readonly ThreadSuggestion[] = [];

type RegenerateState = "idle" | "regenerating" | "failed";

/**
 * Picks the starter questions when the empty state first appears and keeps
 * them until it closes, so a chip never changes under a finger. Generated
 * questions win when there are any. Until the first read of Core's questions
 * answers, no chips are offered, because the choice cannot change once shown.
 * Closing the empty state reads Core again, so the next empty conversation
 * sees newer questions. Asking for other questions replaces the shown ones
 * only when Core returns some, and only while the empty state is still open.
 */
export const useStarterQuestionSelection = ({
  empty,
  loading,
  served,
  pending,
  enabled,
  refresh,
  regenerate,
}: StarterQuestionSelectionInput): StarterQuestionSelection => {
  const open = empty && !loading && !pending;
  const [selection, setSelection] = useState<
    readonly ThreadSuggestion[] | null
  >(null);
  const [regenerateState, setRegenerateState] =
    useState<RegenerateState>("idle");
  if (open && selection === null) {
    setSelection(served?.length ? served : FALLBACK_STARTER_QUESTIONS);
  } else if (!empty && selection !== null) {
    setSelection(null);
    setRegenerateState("idle");
  }
  const shown = useRef(false);
  // Counts closed empty states, so a request answered after its empty state
  // closed changes nothing in the next one.
  const closures = useRef(0);
  useEffect(() => {
    if (open) {
      shown.current = true;
    } else if (!empty && shown.current) {
      shown.current = false;
      closures.current += 1;
      refresh();
    }
  }, [open, empty, refresh]);
  const requestOther = useCallback(async () => {
    const requestedIn = closures.current;
    setRegenerateState("regenerating");
    try {
      const next = await regenerate();
      if (closures.current !== requestedIn) {
        return;
      }
      if (next.length) {
        setSelection(next);
      }
      setRegenerateState("idle");
    } catch {
      if (closures.current === requestedIn) {
        setRegenerateState("failed");
      }
    }
  }, [regenerate]);
  return {
    questions: selection ?? NO_STARTER_QUESTIONS,
    canRegenerate: enabled && selection !== null,
    regenerating: regenerateState === "regenerating",
    regenerateFailed: regenerateState === "failed",
    regenerate: requestOther,
  };
};
