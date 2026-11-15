import { TileFrame, tileActionClass } from "../TileFrame";
import { Reading } from "../Reading";
import { coverSourceNote, CoverStatus } from "./CoverStatus";
import { useCover } from "./data";
import type { CoverProps } from "./schema";
import { coverTileWord } from "./words";
import { cn } from "../../../../lib/utils";

type Verb = "open" | "close" | "stop" | "none";

/**
 * A whole-card press for grids of quick switches: it opens a closed cover,
 * closes an open or partly open one, and stops one that is moving. Every row
 * is one fixed line, so the tile never changes height while in use.
 */
export function CoverTile({ props }: { props: CoverProps }) {
  const cover = useCover(props);
  const { data, status, title } = cover;
  const busy = cover.setPending || cover.moving;
  const verb: Verb = busy ? (cover.canStop ? "stop" : "none") : !cover.canSet ? "none" : data && data.position > 0 ? "close" : "open";
  const word = data ? coverTileWord(data.position, data.motion) : "—";
  const position = data ? `${data.position} % open` : undefined;
  const interactive = verb !== "none";
  const verbText = verb === "stop" ? "Press to stop" : verb === "open" ? "Press to open" : verb === "close" ? "Press to close" : busy ? "Moving" : cover.entity ? "Read-only" : "";
  const press = () => verb === "stop" ? cover.stop() : verb === "open" ? cover.move(100, "tile") : verb === "close" ? cover.move(0, "tile") : undefined;
  const open = !!data && status.value === "live" && data.position > 0;
  const reading = <Reading as="span" value={status.value} className="block overflow-hidden text-ellipsis whitespace-nowrap @max-[200px]:text-reading-narrow">{word}</Reading>;
  const rowClass = "m-0 h-[21px] overflow-hidden text-ellipsis whitespace-nowrap text-label leading-normal";
  const onColor = open && "text-[color-mix(in_oklch,var(--violet)_14%,var(--gray-9))]";
  return <TileFrame title={title} classNames={{ body: "gap-0.5" }} on={open} interactive={interactive} status={status.entries}
    observedAt={data?.observed_at} arrival={data && { updatedAt: data.observed_at, shown: `${word}|${data.position}`, eligible: status.glows }}
    sources={cover.sources} sourceExtra={coverSourceNote}>
    {interactive
      ? <button type="button" data-slot="tile-action" className={tileActionClass} aria-label={`${title}, ${word}${position ? `, ${position}` : ""}. ${verbText}`}
          aria-busy={busy || undefined} title={cover.line("tile")?.text} disabled={!cover.enabled} onClick={press}>{reading}</button>
      : <p>{reading}</p>}
    <p data-slot="cover-row" data-cover-position className={cn(rowClass, "text-gray-9 tabular-nums data-[value=dimmed]:text-gray-8", onColor)} data-value={status.value}>{position ?? " "}</p>
    <p data-slot="cover-row" className={cn(rowClass, "text-gray-8", onColor)}>{verbText || " "}</p>
    <CoverStatus line={cover.line("tile")} tile on={open} />
  </TileFrame>;
}
