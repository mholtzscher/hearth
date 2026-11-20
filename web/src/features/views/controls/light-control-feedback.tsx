import type { CommandFailure } from "../../../api/commands";
import { useControls } from "../data/provider";
import {
  controlStatusClasses,
  controlStatusLineClasses,
  lightFailures,
} from "./control-status";
import { colorHex, readColor } from "./light-color";
import type { LightColorControls } from "./light-color";
import type { ControlState } from "./queue";
import { StatusDot } from "./status-dot";

const shortFailures: Record<CommandFailure["kind"], string> = {
  disabled: "disabled",
  unreachable: "unreachable",
  rejected: "rejected",
  unconfirmed: "unconfirmed",
  network: "not confirmed",
  invalid: "failed",
  "not-found": "not found",
  internal: "failed",
};

interface FeedbackEntry {
  id: string;
  name: string;
  pending: (value: unknown) => string;
}

type ActiveControlState = Exclude<ControlState, { phase: "idle" }>;
type ActiveFeedback = FeedbackEntry & { state: ActiveControlState };

const feedbackEntries = ({
  controls,
  powerId,
  brightnessId,
}: {
  controls: LightColorControls;
  powerId: string | undefined;
  brightnessId: string | undefined;
}): FeedbackEntry[] => {
  const { color } = controls;
  return [
    ...(powerId
      ? [
          {
            id: powerId,
            name: "Power",
            pending: (value: unknown) =>
              value ? "Turning on…" : "Turning off…",
          },
        ]
      : []),
    ...(brightnessId
      ? [
          {
            id: brightnessId,
            name: "Brightness",
            pending: (value: unknown) =>
              `Setting brightness to ${String(value)} %…`,
          },
        ]
      : []),
    ...(controls.temperature
      ? [
          {
            id: controls.temperature.entity.id,
            name: "White",
            pending: (value: unknown) =>
              `Setting white to ${String(value)} mired…`,
          },
        ]
      : []),
    ...(color
      ? [
          {
            id: color.entity.id,
            name: "Color",
            pending: (value: unknown) => {
              const parsed = readColor(value, color.kind);
              return parsed
                ? `Setting color to ${colorHex(parsed)}…`
                : "Setting color…";
            },
          },
        ]
      : []),
  ];
};

const feedbackSummary = (
  active: ActiveFeedback[],
  failed: ActiveFeedback[],
  pendingCount: number
) => {
  if (active.length > 1) {
    return [
      failed.length ? `${failed.length} failed` : "",
      pendingCount ? `${pendingCount} pending` : "",
    ]
      .filter(Boolean)
      .join(" · ");
  }
  const [single] = active;
  if (!single) {
    return "";
  }
  if (single.state.phase === "failed") {
    return `${single.name} ${shortFailures[single.state.failure.kind]}`;
  }
  if (single.name === "Power") {
    return single.pending(single.state.requested);
  }
  return `Setting ${single.name.toLowerCase()}…`;
};

const DetailedFeedback = ({ active }: { active: ActiveFeedback[] }) => (
  <div>
    {active.map(({ id, name, pending, state }) => (
      <div
        data-slot="control-status"
        className="text-label text-gray-9 mt-2 min-h-[21px]"
        key={id}
      >
        <p
          className={controlStatusLineClasses}
          role={state.phase === "failed" ? "alert" : "status"}
          data-tone={state.phase === "failed" ? "error" : "info"}
        >
          <StatusDot tone={state.phase === "failed" ? "error" : "info"} />
          {state.phase === "failed"
            ? `${name}: ${lightFailures[state.failure.kind]}`
            : pending(state.requested)}
        </p>
      </div>
    ))}
  </div>
);

const InlineFeedback = ({
  summary,
  hasFailure,
  hidden,
  descriptionId,
}: {
  summary: string;
  hasFailure: boolean;
  hidden: boolean;
  descriptionId: string | undefined;
}) => (
  <span
    data-slot="color-feedback-indicator"
    className="text-label text-info data-[tone=error]:text-destructive whitespace-nowrap"
    aria-hidden={hidden || undefined}
    data-tone={hasFailure ? "error" : "info"}
  >
    <span
      id={descriptionId}
      className="sr-only"
      role={hasFailure ? "alert" : "status"}
    >
      {summary}
    </span>
    {summary && (
      <span aria-hidden="true">{hasFailure ? "Failed" : "Pending"}</span>
    )}
  </span>
);

const SummaryFeedback = ({
  summary,
  hasFailure,
  hidden,
  onDetails,
}: {
  summary: string;
  hasFailure: boolean;
  hidden: boolean;
  onDetails: (() => void) | undefined;
}) => (
  <div
    data-slot="control-status"
    data-light-feedback
    className={controlStatusClasses}
    aria-hidden={hidden || undefined}
    inert={hidden || undefined}
  >
    <p
      className={`${controlStatusLineClasses} whitespace-nowrap`}
      role={hasFailure ? "alert" : "status"}
      data-tone={hasFailure ? "error" : "info"}
    >
      {summary && (
        <button
          type="button"
          className="relative flex max-w-full cursor-pointer items-baseline gap-1.5 p-0 text-start font-[inherit] text-inherit before:absolute before:inset-x-0 before:-inset-y-[11.5px]"
          onClick={onDetails}
        >
          <StatusDot tone={hasFailure ? "error" : "info"} />
          <span className="overflow-hidden text-ellipsis">
            {summary} · Details
          </span>
        </button>
      )}
    </p>
  </div>
);

/** Summarize in an existing card row; keep every Entity's outcome in the picker. */
export const LightControlFeedback = ({
  controls,
  brightnessId,
  powerId,
  detailed = false,
  hidden = false,
  inline = false,
  descriptionId,
  onDetails,
}: {
  controls: LightColorControls;
  brightnessId?: string;
  powerId?: string;
  detailed?: boolean;
  hidden?: boolean;
  inline?: boolean;
  onDetails?: () => void;
  descriptionId?: string;
}) => {
  const entries = feedbackEntries({ controls, powerId, brightnessId });
  const [states] = useControls(
    entries.map((entry) => ({ entityId: entry.id, operation: "set" }))
  );
  const active: ActiveFeedback[] = entries.flatMap((entry, index) => {
    const state = states[index];
    return state.phase === "idle" ? [] : [{ ...entry, state }];
  });
  const failed = active.filter((entry) => entry.state.phase === "failed");
  const pendingCount = active.length - failed.length;
  const summary = feedbackSummary(active, failed, pendingCount);
  if (detailed) {
    return <DetailedFeedback active={active} />;
  }
  if (inline) {
    return (
      <InlineFeedback
        summary={summary}
        hasFailure={failed.length > 0}
        hidden={hidden}
        descriptionId={descriptionId}
      />
    );
  }
  return (
    <SummaryFeedback
      summary={summary}
      hasFailure={failed.length > 0}
      hidden={hidden}
      onDetails={onDetails}
    />
  );
};
