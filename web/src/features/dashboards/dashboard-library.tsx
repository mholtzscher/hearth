import { LayoutDashboardIcon, Trash2Icon } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { useBaseUrlVersion } from "../../api/client";
import { deleteDashboard, listDashboards } from "../../api/dashboards";
import type { DashboardSummary } from "../../api/dashboards";
import { Button } from "../../components/button";
import { Card } from "../../components/card";
import { DecisionDialog } from "../../components/decision-dialog";
import { Notice } from "../../components/notice";
import { PageHeader } from "../../components/page-header";
import { observationAge, useMinuteClock } from "../views/cards/observation";

interface LoadedPage {
  page: DashboardSummary[];
  nextCursor: string | null;
  failure: string | null;
}

const RECENT_DAYS = 7;
const DAY_MS = 24 * 60 * 60 * 1000;

/** Recent edits read as an age; older ones as a short local date. */
const updatedLabel = (updatedAt: string, now: number) => {
  const at = Date.parse(updatedAt);
  if (!Number.isFinite(at)) {
    return;
  }
  if (now - at < RECENT_DAYS * DAY_MS) {
    return observationAge(updatedAt, now, "long");
  }
  const sameYear = new Date(at).getFullYear() === new Date(now).getFullYear();
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    year: sameYear ? undefined : "numeric",
  }).format(at);
};

const fullDate = new Intl.DateTimeFormat(undefined, {
  dateStyle: "long",
  timeStyle: "short",
});

const LibraryCard = ({
  item,
  now,
  onDelete,
}: {
  item: DashboardSummary;
  now: number;
  onDelete: () => void;
}) => {
  const updated = updatedLabel(item.updated_at, now);
  return (
    <Card
      asChild
      className="hover:border-gray-5 has-[a:focus-visible]:outline-ring relative transition-colors duration-150 has-[a:focus-visible]:outline-2 has-[a:focus-visible]:outline-offset-3 has-[a:focus-visible]:outline-solid"
    >
      <article aria-labelledby={`dashboard-${item.id}`}>
        <LayoutDashboardIcon
          className="text-muted-foreground size-5"
          aria-hidden="true"
        />
        <h2
          id={`dashboard-${item.id}`}
          className="text-label mt-3 font-medium [overflow-wrap:anywhere]"
        >
          {/* The link covers the card; Delete sits above it. */}
          <Link
            to={`/dashboards/${encodeURIComponent(item.id)}`}
            className="after:rounded-card outline-none after:absolute after:inset-0"
          >
            {item.title}
          </Link>
        </h2>
        {updated && (
          <p className="text-caption text-muted-foreground mt-1">
            Updated{" "}
            <time
              dateTime={item.updated_at}
              title={fullDate.format(Date.parse(item.updated_at))}
            >
              {updated}
            </time>
          </p>
        )}
        {/* After the title in reading order; drawn level with the icon. */}
        <Button
          variant="ghost"
          size="icon-touch"
          className="absolute end-1.5 top-1.5 z-10"
          aria-label={`Delete ${item.title}`}
          onClick={onDelete}
        >
          <Trash2Icon aria-hidden="true" />
        </Button>
      </article>
    </Card>
  );
};

/** Empty frames at a ready card's height while the first page loads. */
const LoadingCards = () => (
  <>
    {[0, 1, 2].map((slot) => (
      <Card key={slot} aria-hidden="true">
        <div className="h-5" />
        <div className="mt-3 h-[21px]" />
        <div className="mt-1 h-[18px]" />
      </Card>
    ))}
  </>
);

const ConnectedLibrary = () => {
  const [items, setItems] = useState<DashboardSummary[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState<DashboardSummary | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [loading, setLoading] = useState(true);
  const now = useMinuteClock();

  const fetchPage = useCallback(async (next?: string): Promise<LoadedPage> => {
    try {
      const result = await listDashboards(next);
      return {
        page: result.items,
        nextCursor: result.next_cursor ?? null,
        failure: null,
      };
    } catch (error_) {
      return {
        page: [],
        nextCursor: null,
        failure:
          error_ instanceof Error
            ? error_.message
            : "Could not load dashboards.",
      };
    }
  }, []);

  useEffect(() => {
    // The first page starts on mount; server switches remount this library by
    // key, so an old backend's in-flight page cannot land in the new list.
    let cancelled = false;
    const loadFirstPage = async () => {
      const result = await fetchPage();
      if (cancelled) {
        return;
      }
      setLoading(false);
      if (result.failure !== null) {
        setError(result.failure);
        return;
      }
      setItems(result.page);
      setCursor(result.nextCursor);
    };
    loadFirstPage();
    return () => {
      cancelled = true;
    };
  }, [fetchPage]);

  const load = async (next?: string) => {
    const result = await fetchPage(next);
    setLoading(false);
    if (result.failure !== null) {
      setError(result.failure);
      return;
    }
    setItems((previous) =>
      next ? [...previous, ...result.page] : result.page
    );
    setCursor(result.nextCursor);
  };

  const reload = () => {
    setLoading(true);
    setError("");
    load();
  };

  const loadMore = (next: string) => {
    setLoading(true);
    setError("");
    load(next);
  };

  const remove = async (item: DashboardSummary) => {
    setDeleting(true);
    try {
      await deleteDashboard(item.id, item.revision);
    } catch (error_) {
      setError(
        error_ instanceof Error
          ? `${error_.message} Reload the library before trying again.`
          : "Could not delete dashboard."
      );
      setConfirming(null);
      setDeleting(false);
      return;
    }
    setConfirming(null);
    setDeleting(false);
    setLoading(true);
    setError("");
    await load();
  };

  const firstLoad = loading && items.length === 0;

  return (
    <section className="flex-1 overflow-auto">
      <div className="max-tablet:px-5 max-tablet:pt-4 mx-auto max-w-[1440px] px-7 pt-[22px] pb-10">
        <PageHeader
          title="Dashboards"
          description="Useful views, kept close and connected to your home."
        />
        {error && (
          <Notice
            role="alert"
            className="mt-0 mb-4 flex flex-wrap items-center justify-between gap-x-4 gap-y-2"
          >
            <span className="min-w-0">{error}</span>
            <Button
              variant="outline"
              size="touch"
              onClick={() => {
                reload();
              }}
            >
              Reload
            </Button>
          </Notice>
        )}
        {!(loading || error) && items.length === 0 && (
          <Card>
            <h2 className="text-label mb-1 font-medium">
              Your first dashboard starts with a question.
            </h2>
            <p className="text-caption text-muted-foreground mb-4">
              Generate a view in conversation, customize it, and save it here.
            </p>
            <Link className="text-link" to="/">
              Start a conversation →
            </Link>
          </Card>
        )}
        {(items.length > 0 || firstLoad) && (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,250px),1fr))] gap-3">
            {items.map((item) => (
              <LibraryCard
                key={item.id}
                item={item}
                now={now}
                onDelete={() => setConfirming(item)}
              />
            ))}
            {firstLoad && <LoadingCards />}
          </div>
        )}
        {cursor && (
          <Button
            variant="outline"
            size="touch"
            className="mt-4"
            disabled={loading}
            onClick={() => {
              loadMore(cursor);
            }}
          >
            Load more
          </Button>
        )}
        {loading && <output className="sr-only">Loading dashboards…</output>}
      </div>
      {confirming && (
        <DecisionDialog
          title="Remove this dashboard?"
          onClose={() => {
            if (!deleting) {
              setConfirming(null);
            }
          }}
        >
          <p className="text-muted-foreground mt-[14px] mb-5">
            {confirming.title} will be deleted. Your home data and conversation
            remain unchanged.
          </p>
          <div className="flex flex-wrap justify-end gap-2">
            <Button
              variant="outline"
              size="touch"
              disabled={deleting}
              onClick={() => setConfirming(null)}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="touch"
              disabled={deleting}
              onClick={() => {
                remove(confirming);
              }}
            >
              Delete dashboard
            </Button>
          </div>
        </DecisionDialog>
      )}
    </section>
  );
};

export const DashboardLibrary = () => {
  const backendVersion = useBaseUrlVersion();
  return <ConnectedLibrary key={backendVersion} />;
};
