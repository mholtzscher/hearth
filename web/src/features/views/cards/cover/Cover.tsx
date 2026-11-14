import { ArrowDownToLineIcon, ArrowUpToLineIcon, SquareIcon } from "lucide-react";
import { CardFrame } from "../CardFrame";
import { CoverDiagram } from "./CoverDiagram";
import { CoverTile } from "./CoverTile";
import { useCover } from "./data";
import { coverSourceNote, CoverStatus } from "./CoverStatus";
import type { CoverProps } from "./schema";
import { coverWord } from "./words";

export function CoverCard({ props }: { props: CoverProps }) {
  const cover = useCover(props);
  const { data, status, title } = cover;
  const word = data ? coverWord(data.position, data.motion) : "—";
  const confirming = cover.confirm?.from === "button" && cover.confirm.target === 100;
  const disabled = !cover.enabled;
  return <CardFrame title={title} className="view-cover" status={status.entries} observedAt={data?.observed_at}
    arrival={data && { updatedAt: data.observed_at, shown: `${word}|${data.position}`, eligible: status.glows }}
    sources={cover.sources} sourceExtra={coverSourceNote}>
    <div className="view-cover-lead">
      <CoverDiagram position={data?.position} target={cover.target} value={status.value} />
      <div className="view-cover-reading">
        <p className="view-metric-value" data-value={status.value}>{word}</p>
        <p className="view-cover-position" data-value={status.value}>{data ? `${data.position} % open` : " "}</p>
      </div>
    </div>
    {(cover.canSet || cover.canStop) && <>
      <div className="view-cover-actions" role="group" aria-label={`${title} controls`}>
        {cover.canSet && <button type="button" className="view-cover-action" data-requested={cover.target === 100 || undefined} data-confirm={confirming || undefined}
          aria-label={confirming ? `Confirm open ${title}` : `Open ${title}`} disabled={disabled} onClick={() => cover.move(100, "button")}>
          {!confirming && <ArrowUpToLineIcon aria-hidden="true" />}<span>{confirming ? "Confirm" : "Open"}</span>
        </button>}
        {cover.canStop && <button type="button" className="view-cover-action" aria-label={`Stop ${title}`} disabled={disabled} onClick={cover.stop}>
          <SquareIcon aria-hidden="true" /><span>Stop</span>
        </button>}
        {cover.canSet && <button type="button" className="view-cover-action" data-requested={cover.target === 0 || undefined}
          aria-label={`Close ${title}`} disabled={disabled} onClick={() => cover.move(0, "button")}>
          <ArrowDownToLineIcon aria-hidden="true" /><span>Close</span>
        </button>}
      </div>
      <CoverStatus line={cover.line("button")} />
    </>}
  </CardFrame>;
}

export function BoundCover(props: CoverProps) {
  return props.layout === "tile" ? <CoverTile props={props} /> : <CoverCard props={props} />;
}
