import type { ThreadSuggestion } from "@assistant-ui/react";
import { useEffect, useRef, useState } from "react";

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
  /** Reads Core's questions again, keeping the current ones on failure. */
  refresh: () => void;
}

const NO_STARTER_QUESTIONS: readonly ThreadSuggestion[] = [];

/**
 * Picks the starter questions when the empty state first appears and keeps
 * them until it closes, so a chip never changes under a finger. Generated
 * questions win when there are any. Until the first read of Core's questions
 * answers, no chips are offered, because the choice cannot change once shown.
 * Closing the empty state reads Core again, so the next empty conversation
 * sees newer questions.
 */
export const useStarterQuestionSelection = ({
  empty,
  loading,
  served,
  pending,
  refresh,
}: StarterQuestionSelectionInput): readonly ThreadSuggestion[] => {
  const open = empty && !loading && !pending;
  const [selection, setSelection] = useState<
    readonly ThreadSuggestion[] | null
  >(null);
  if (open && selection === null) {
    setSelection(served?.length ? served : FALLBACK_STARTER_QUESTIONS);
  } else if (!empty && selection !== null) {
    setSelection(null);
  }
  const shown = useRef(false);
  useEffect(() => {
    if (open) {
      shown.current = true;
    } else if (!empty && shown.current) {
      shown.current = false;
      refresh();
    }
  }, [open, empty, refresh]);
  return selection ?? NO_STARTER_QUESTIONS;
};
