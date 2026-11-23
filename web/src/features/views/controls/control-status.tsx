import { useState } from "react";
import type { ReactNode } from "react";

import type { CommandFailure } from "../../../api/commands";
import { cn } from "../../../lib/utils";
import { useControl } from "../data/provider";
import { statusFade } from "./command-motion";
import { StatusDot } from "./status-dot";

export const controlStatusClasses =
  "pending-status-handoff relative mt-2 h-[21px] min-w-0 text-label text-gray-9";
export const controlStatusLineClasses =
  "flex min-w-0 items-baseline gap-1.5 empty:hidden data-[tone=error]:text-destructive";

/** Keep only the departing words, not the live status, dot, or hit target. */
export const PendingStatusExit = ({ text }: { text?: string }) => {
  const [display, setDisplay] = useState<{
    current?: string;
    leaving?: string;
  }>({ current: text });
  if (text !== display.current) {
    setDisplay({ current: text, leaving: text ? undefined : display.current });
  }
  if (!display.leaving) {
    return null;
  }
  return (
    <span
      aria-hidden="true"
      data-slot="pending-status-exit"
      data-exit-text={display.leaving}
      data-reduced-motion="fade"
      className="animate-fade-out pointer-events-none absolute inset-x-0 top-0 truncate ps-3 opacity-0 before:content-[attr(data-exit-text)]"
      onAnimationEnd={() => setDisplay({ current: text })}
    />
  );
};

export const shortCommandFailures: Record<CommandFailure["kind"], string> = {
  disabled: "Disabled.",
  unreachable: "Unreachable.",
  rejected: "Rejected.",
  unconfirmed: "Not confirmed.",
  network: "Connection lost.",
  invalid: "Invalid change.",
  "not-found": "Not found.",
  internal: "Failed.",
};

export const ControlStatusText = ({
  full,
  short = full,
  slot,
  fade = false,
}: {
  full: ReactNode;
  short?: ReactNode;
  slot?: string;
  fade?: boolean;
}) => (
  <>
    <span
      className={cn("min-w-0 truncate", fade && statusFade)}
      data-reduced-motion={fade ? "fade" : undefined}
      data-slot={slot ?? "control-status-text"}
      aria-hidden={short !== full || undefined}
      title={typeof full === "string" ? full : undefined}
    >
      {short}
    </span>
    {short !== full && <span className="sr-only">{full}</span>}
  </>
);

export const lightFailures: Record<CommandFailure["kind"], string> = {
  disabled: "Couldn't change the light. This Entity is disabled.",
  unreachable: "Couldn't reach the light. Its adapter or the light is offline.",
  rejected: "The light rejected the change.",
  unconfirmed: "The light didn't confirm in time. It may still change.",
  network: "Couldn't reach Hearth. The change may still apply.",
  invalid: "Couldn't change the light.",
  "not-found": "Couldn't change the light.",
  internal: "Couldn't change the light.",
};

export const powerFailures: Record<CommandFailure["kind"], string> = {
  disabled: "This Entity is disabled.",
  unreachable: "Couldn't reach this Device.",
  rejected: "Change rejected. Try again.",
  unconfirmed: "No confirmation. It may still change.",
  network: "Couldn't reach Hearth. The change may still apply.",
  invalid: "Couldn't change this Device.",
  "not-found": "Couldn't change this Device.",
  internal: "Couldn't change this Device.",
};

/**
 * The line under a control while its Command is pending, or after it failed.
 * Its row is always reserved, so a Command that settles in milliseconds
 * doesn't move the card. Pending text is a polite status; a failure is an
 * alert. Both pair a tone dot with words, and the failure stays until the next
 * request.
 */
export const ControlStatus = ({
  entityId,
  pending,
  subject = "light",
  idle,
  className,
}: {
  entityId: string;
  pending: (requested: unknown) => string;
  subject?: "light" | "power";
  idle?: ReactNode;
  className?: string;
}) => {
  const [state] = useControl({ entityId, operation: "set" });
  const working = state.phase === "pending" || state.phase === "confirming";
  const pendingText = working ? pending(state.requested) : undefined;
  return (
    <div
      data-slot="control-status"
      className={cn(controlStatusClasses, className)}
    >
      <PendingStatusExit text={pendingText} />
      <output data-tone="info" className={controlStatusLineClasses}>
        {pendingText !== undefined && (
          <>
            <StatusDot tone="info" pending />
            <ControlStatusText key={pendingText} full={pendingText} fade />
          </>
        )}
        {state.phase === "idle" && idle && <ControlStatusText full={idle} />}
      </output>
      {state.phase === "failed" && (
        <p role="alert" data-tone="error" className={controlStatusLineClasses}>
          <StatusDot tone="error" />
          <ControlStatusText
            full={
              (subject === "power" ? powerFailures : lightFailures)[
                state.failure.kind
              ]
            }
            short={shortCommandFailures[state.failure.kind]}
            fade
          />
        </p>
      )}
    </div>
  );
};
