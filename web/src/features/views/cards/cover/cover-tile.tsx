import { cn } from "../../../../lib/utils";
import { useRefusal } from "../../controls/command-motion";
import { Reading } from "../reading";
import { TileFrame, tileActionClass } from "../tile-frame";
import { coverSourceNote, CoverStatus } from "./cover-status";
import { useCover } from "./data";
import type { CoverProps } from "./schema";
import { coverTileWord } from "./words";

type Verb = "open" | "close" | "stop" | "none";

const tileVerb = (
  busy: boolean,
  canStop: boolean,
  canSet: boolean,
  position: number | undefined
): Verb => {
  if (busy) {
    return canStop ? "stop" : "none";
  }
  if (!canSet) {
    return "none";
  }
  return position !== undefined && position > 0 ? "close" : "open";
};

const tileVerbText = (
  verb: Verb,
  busy: boolean,
  hasEntity: boolean
): string => {
  if (verb === "stop") {
    return "Press to stop";
  }
  if (verb === "open") {
    return "Press to open";
  }
  if (verb === "close") {
    return "Press to close";
  }
  if (busy) {
    return "Moving";
  }
  return hasEntity ? "Read-only" : "";
};

/**
 * A whole-card press for grids of quick switches: it opens a closed cover,
 * closes an open or partly open one, and stops one that is moving. Every row
 * is one fixed line, so the tile never changes height while in use.
 */
export const CoverTile = ({ props }: { props: CoverProps }) => {
  const cover = useCover(props);
  const line = cover.line("tile");
  const setRefusal = useRefusal(cover.setState);
  const stopRefusal = useRefusal(cover.stopState);
  const { data, status, title } = cover;
  const busy = cover.setPending || cover.moving;
  const verb = tileVerb(busy, cover.canStop, cover.canSet, data?.position);
  const word = data ? coverTileWord(data.position, data.motion) : "—";
  const position = data ? `${data.position} % open` : undefined;
  const interactive = verb !== "none";
  const verbText = tileVerbText(verb, busy, !!cover.entity);
  const press = () => {
    if (verb === "stop") {
      cover.stop();
      return;
    }
    if (verb === "open") {
      cover.move(100, "tile");
      return;
    }
    if (verb === "close") {
      cover.move(0, "tile");
    }
  };
  const open = !!data && status.value === "live" && data.position > 0;
  const reading = (
    <Reading
      as="span"
      value={status.value}
      className="block overflow-hidden text-ellipsis whitespace-nowrap"
    >
      {word}
    </Reading>
  );
  const rowClass =
    "m-0 h-[21px] overflow-hidden text-ellipsis whitespace-nowrap text-label";
  const onColor =
    open && "text-[color-mix(in_oklch,var(--violet)_14%,var(--gray-9))]";
  return (
    <TileFrame
      title={title}
      className={setRefusal ?? stopRefusal}
      classNames={{ body: "gap-0.5" }}
      on={open}
      interactive={interactive}
      working={busy}
      stateObservedAt={data?.observed_at}
      status={status.entries}
      observedAt={data?.observed_at}
      arrival={
        data && {
          updatedAt: data.observed_at,
          shown: `${word}|${data.position}`,
          eligible: status.glows,
        }
      }
      sources={cover.sources}
      sourceExtra={coverSourceNote}
    >
      {interactive ? (
        <button
          type="button"
          data-slot="tile-action"
          className={tileActionClass}
          aria-label={`${title}, ${word}${position ? `, ${position}` : ""}. ${verbText}`}
          aria-busy={busy || undefined}
          title={line?.text}
          disabled={!cover.enabled}
          onClick={press}
        >
          {reading}
        </button>
      ) : (
        <p>{reading}</p>
      )}
      <p
        data-slot="cover-row"
        data-cover-position
        className={cn(
          rowClass,
          "text-gray-9 data-[value=dimmed]:text-gray-8",
          onColor
        )}
        data-value={status.value}
      >
        {position ?? " "}
      </p>
      <p data-slot="cover-row" className={cn(rowClass, "text-gray-8", onColor)}>
        {verbText || " "}
      </p>
      <CoverStatus line={line} tile on={open} />
    </TileFrame>
  );
};
