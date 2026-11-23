import { cn } from "../../../../lib/utils";
import {
  controlStatusClasses,
  controlStatusLineClasses,
  ControlStatusText,
  PendingStatusExit,
} from "../../controls/control-status";
import { StatusDot } from "../../controls/status-dot";
import type { CoverLine } from "./data";

/** Position, motion, and what a finished Command does and doesn't establish. */
export const coverSourceNote = (
  <>
    <p>
      Position and motion come from the cover&apos;s reported State. Some covers
      estimate position from travel time, so 0 % can also mean the position was
      never set.
    </p>
    <p>
      A finished Open or Close means the cover reported that position, not that
      closure was verified. Stop completes once it is sent: the reading changes
      when the cover reports. Stop isn&apos;t an emergency stop and has no
      timing guarantee.
    </p>
  </>
);

const pendingCoverText = (line: CoverLine | undefined, tile: boolean) => {
  if (line?.tone !== "info" || line.alert) {
    return;
  }
  return tile ? line.short : line.text;
};

/** The reserved line beneath a cover's controls. Pending words are a polite status; a failure is an alert. */
export const CoverStatus = ({
  line,
  tile = false,
  on = false,
}: {
  line?: CoverLine;
  tile?: boolean;
  on?: boolean;
}) => {
  const words = line && (
    <ControlStatusText
      key={line.text}
      full={line.text}
      fade={line.tone === "info" || line.alert}
      short={tile || line.alert ? line.short : undefined}
      slot={tile ? "cover-short" : undefined}
    />
  );
  const status = line?.alert ? undefined : line;
  return (
    <div
      data-cover-status
      data-slot={tile ? "cover-row" : undefined}
      className={cn(
        controlStatusClasses,
        tile && "m-0",
        on && "text-[color-mix(in_oklch,var(--violet)_14%,var(--gray-9))]"
      )}
    >
      <PendingStatusExit text={pendingCoverText(status, tile)} />
      <output
        className={cn(
          controlStatusLineClasses,
          "data-[tone=warn]:text-warning"
        )}
        data-tone={status?.tone}
      >
        {status && (
          <>
            <StatusDot tone={status.tone} pending={status.tone === "info"} />
            {words}
          </>
        )}
      </output>
      {line?.alert && (
        <p className={controlStatusLineClasses} role="alert" data-tone="error">
          <StatusDot tone="error" />
          {words}
        </p>
      )}
    </div>
  );
};
