import { useAuiState } from "@assistant-ui/react";
import {
  createContext,
  useContext,
  useLayoutEffect,
  useMemo,
  useRef,
} from "react";
import type { ReactNode } from "react";

/** Arrive (DESIGN.md, Motion): a message fades in while rising 8px. */
export const arriveClass = "animate-arrive motion-reduce:animate-fade";
/** A block inside an arriving reply rises 4px. */
const blockArriveClass =
  "animate-arrive [--motion-rise:4px] motion-reduce:animate-fade";
/** Resolve: reply text fades in from a 3px blur. */
export const resolveClass = "animate-resolve motion-reduce:animate-fade";

interface ArrivalTracker {
  arrives: (index: number) => boolean;
}
const ArrivalContext = createContext<ArrivalTracker>({ arrives: () => false });

/**
 * The Quiet Load Rule: the history a conversation opens with stays still. The
 * thread settles once its history has loaded and the rendered thread has caught
 * up with it. After that, a message arrives only at a position the thread has
 * not shown before, so a queued prompt that is sent, or a live reply replaced
 * by its stored copy, does not arrive twice.
 */
export const ArrivalProvider = ({
  threadKey,
  historyLoading,
  messageCount,
  children,
}: {
  threadKey: string;
  /** The conversation's own loading state; the rendered thread can lag it. */
  historyLoading: boolean;
  /** How many messages the conversation holds. */
  messageCount: number;
  children: ReactNode;
}) => {
  const count = useAuiState((state) => state.thread.messages.length);
  const shown = useRef({ key: threadKey, settled: false, last: -1 });
  useLayoutEffect(() => {
    if (shown.current.key !== threadKey) {
      shown.current = { key: threadKey, settled: false, last: -1 };
    }
    if (!historyLoading && count === messageCount) {
      shown.current.settled = true;
    }
    shown.current.last = Math.max(shown.current.last, count - 1);
  }, [count, historyLoading, messageCount, threadKey]);
  const tracker = useMemo<ArrivalTracker>(
    () => ({
      arrives: (index) =>
        shown.current.key === threadKey &&
        shown.current.settled &&
        index > shown.current.last,
    }),
    [threadKey]
  );
  return (
    <ArrivalContext.Provider value={tracker}>
      {children}
    </ArrivalContext.Provider>
  );
};

/** Whether this message arrives; the tracker is stable, so the answer holds for the mounted message. */
export const useMessageArrival = (): boolean => {
  const { arrives } = useContext(ArrivalContext);
  const index = useAuiState((state) => state.message.index);
  return useMemo(() => arrives(index), [arrives, index]);
};

const ReplyArrivalContext = createContext(false);

/** Lets the blocks of an arriving reply arrive as they appear. */
export const ReplyArrival = ReplyArrivalContext.Provider;

export interface BlockArrivalProps {
  className: string;
  "data-reduced-motion": "fade";
}

const blockArrival: BlockArrivalProps = {
  className: blockArriveClass,
  "data-reduced-motion": "fade",
};

/** A block that appears in an arriving reply rises 4px. */
export const useBlockArrival = (): BlockArrivalProps | undefined =>
  useContext(ReplyArrivalContext) ? blockArrival : undefined;

/** Whether this reply is arriving, so what appears in it moves. */
export const useReplyArrival = (): boolean => useContext(ReplyArrivalContext);
