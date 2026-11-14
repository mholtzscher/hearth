import { CardFrame } from "../CardFrame";
import { coverSourceNote, CoverStatus } from "./CoverStatus";
import { useCover } from "./data";
import type { CoverProps } from "./schema";
import { coverTileWord } from "./words";

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
  const reading = <span className="view-metric-value" data-value={status.value}>{word}</span>;
  return <CardFrame title={title} className={`view-toggle-tile view-cover-tile${open ? " is-on" : ""}${interactive ? " is-interactive" : ""}`} status={status.entries} compactTimeLabel
    observedAt={data?.observed_at} arrival={data && { updatedAt: data.observed_at, shown: `${word}|${data.position}`, eligible: status.glows }}
    sources={cover.sources} sourceExtra={coverSourceNote}>
    {interactive
      ? <button type="button" className="view-toggle-tile-action" aria-label={`${title}, ${word}${position ? `, ${position}` : ""}. ${verbText}`}
          aria-busy={busy || undefined} title={cover.line("tile")?.text} disabled={!cover.enabled} onClick={press}>{reading}</button>
      : <p>{reading}</p>}
    <p className="view-cover-row view-cover-position" data-value={status.value}>{position ?? " "}</p>
    <p className="view-cover-row view-cover-verb">{verbText || " "}</p>
    <CoverStatus line={cover.line("tile")} tile />
  </CardFrame>;
}
