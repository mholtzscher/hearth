import {
  ArrowDownToLineIcon,
  ArrowUpToLineIcon,
  SquareIcon,
} from "lucide-react";

import { Button } from "../../../../components/button";
import { DataCard } from "../data-card";
import { Reading } from "../reading";
import { CoverDiagram } from "./cover-diagram";
import { coverSourceNote, CoverStatus } from "./cover-status";
import { CoverTile } from "./cover-tile";
import { useCover } from "./data";
import type { CoverProps } from "./schema";
import { coverWord } from "./words";

export const CoverCard = ({ props }: { props: CoverProps }) => {
  const cover = useCover(props);
  const { data, status, title } = cover;
  const word = data ? coverWord(data.position, data.motion) : "—";
  const confirming =
    cover.confirm?.from === "button" && cover.confirm.target === 100;
  const disabled = !cover.enabled;
  return (
    <DataCard
      title={title}
      className="@container"
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
      <div className="grid grid-cols-[88px_minmax(0,1fr)] items-center gap-4 pb-4 @max-[250px]:grid-cols-[64px_minmax(0,1fr)] @max-[250px]:gap-3">
        <CoverDiagram
          position={data?.position}
          target={cover.target}
          value={status.value}
        />
        <div className="min-w-0">
          <div data-cover-reading>
            <Reading
              value={status.value}
              className="@max-[250px]:text-reading-narrow [overflow-wrap:anywhere]"
            >
              {word}
            </Reading>
          </div>
          <p
            data-slot="cover-position"
            className="text-label text-gray-9 data-[value=dimmed]:text-gray-8 tabular-nums"
            data-value={status.value}
          >
            {data ? `${data.position} % open` : " "}
          </p>
        </div>
      </div>
      {(cover.canSet || cover.canStop) && (
        <>
          <fieldset
            className="m-0 grid min-w-0 grid-cols-[repeat(auto-fit,minmax(0,1fr))] gap-2 border-0 p-0"
            aria-label={`${title} controls`}
          >
            {cover.canSet && (
              <Button
                variant={confirming ? "default" : "outline"}
                size="touch"
                type="button"
                className="data-[requested]:border-violet data-[requested]:bg-violet-soft data-[requested]:text-violet-text data-[confirm]:border-primary data-[confirm]:bg-primary data-[confirm]:text-primary-foreground data-[confirm]:hover:bg-orange-hover min-w-0 touch-manipulation px-2.5 data-[confirm]:transition-none @max-[250px]:gap-1 @max-[250px]:px-1"
                data-requested={cover.target === 100 || undefined}
                data-confirm={confirming || undefined}
                aria-label={
                  confirming ? `Confirm open ${title}` : `Open ${title}`
                }
                disabled={disabled}
                onClick={() => cover.move(100, "button")}
              >
                {!confirming && <ArrowUpToLineIcon aria-hidden="true" />}
                <span className="min-w-0 overflow-hidden text-ellipsis">
                  {confirming ? "Confirm" : "Open"}
                </span>
              </Button>
            )}
            {cover.canStop && (
              <Button
                variant="outline"
                size="touch"
                type="button"
                className="min-w-0 touch-manipulation px-2.5 @max-[250px]:gap-1 @max-[250px]:px-1"
                aria-label={`Stop ${title}`}
                disabled={disabled}
                onClick={() => cover.stop()}
              >
                <SquareIcon aria-hidden="true" />
                <span className="min-w-0 overflow-hidden text-ellipsis">
                  Stop
                </span>
              </Button>
            )}
            {cover.canSet && (
              <Button
                variant="outline"
                size="touch"
                type="button"
                className="data-[requested]:border-violet data-[requested]:bg-violet-soft data-[requested]:text-violet-text min-w-0 touch-manipulation px-2.5 @max-[250px]:gap-1 @max-[250px]:px-1"
                data-requested={cover.target === 0 || undefined}
                aria-label={`Close ${title}`}
                disabled={disabled}
                onClick={() => cover.move(0, "button")}
              >
                <ArrowDownToLineIcon aria-hidden="true" />
                <span className="min-w-0 overflow-hidden text-ellipsis">
                  Close
                </span>
              </Button>
            )}
          </fieldset>
          <CoverStatus line={cover.line("button")} />
        </>
      )}
    </DataCard>
  );
};

export const BoundCover = (props: CoverProps) =>
  props.layout === "tile" ? (
    <CoverTile props={props} />
  ) : (
    <CoverCard props={props} />
  );
