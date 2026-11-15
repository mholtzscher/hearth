import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { useBaseUrlVersion } from "../../api/client";
import { deleteDashboard, listDashboards } from "../../api/dashboards";
import type { DashboardSummary } from "../../api/dashboards";
import { Button } from "../../components/button";
import { Card } from "../../components/card";
import { DecisionDialog } from "../../components/decision-dialog";
import { Notice } from "../../components/notice";

interface LoadedPage {
  page: DashboardSummary[];
  nextCursor: string | null;
  failure: string | null;
}

const ConnectedLibrary = () => {
  const [items, setItems] = useState<DashboardSummary[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState<DashboardSummary | null>(null);
  const [loading, setLoading] = useState(true);

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
    try {
      await deleteDashboard(item.id, item.revision);
      setConfirming(null);
      setLoading(true);
      setError("");
      await load();
    } catch (error_) {
      setError(
        error_ instanceof Error
          ? `${error_.message} Reload the library before trying again.`
          : "Could not delete dashboard."
      );
      setConfirming(null);
    }
  };

  return (
    <section className="max-tablet:px-5 max-tablet:py-6 flex-1 overflow-auto p-[34px]">
      <p className="text-muted-foreground text-[10px] font-[650] tracking-[.13em] uppercase">
        Your dashboards
      </p>
      <h1 className="text-headline leading-[1.3] font-medium tracking-[-.03em] text-balance [overflow-wrap:anywhere]">
        Made for you. Here to stay.
      </h1>
      <p className="text-muted-foreground mt-2 mb-[25px]">
        Useful views, kept close and connected to your home.
      </p>
      {error && (
        <Notice role="alert">
          {error}{" "}
          <Button
            variant="outline"
            size="touch"
            className="m-1.5"
            onClick={() => {
              reload();
            }}
          >
            Reload
          </Button>
        </Notice>
      )}
      {!loading && items.length === 0 && (
        <Card>
          <h2 className="text-label mt-3.5 mb-1">
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
      <div className="grid grid-cols-[repeat(auto-fill,minmax(250px,1fr))] gap-3.5">
        {items.map((item) => (
          <Card asChild key={item.id}>
            <article>
              <span aria-hidden="true">◫</span>
              <h2 className="text-label mt-3.5 mb-1">{item.title}</h2>
              <p className="text-caption text-muted-foreground mb-4">
                Revision {item.revision} · Updated{" "}
                {new Date(item.updated_at).toLocaleDateString()}
              </p>
              <div className="flex flex-wrap items-center gap-2">
                <Button asChild variant="outline" size="touch">
                  <Link to={`/dashboards/${encodeURIComponent(item.id)}`}>
                    Open dashboard →
                  </Link>
                </Button>
                <Button
                  variant="outline"
                  size="touch"
                  aria-label={`Delete ${item.title}`}
                  onClick={() => setConfirming(item)}
                >
                  Delete
                </Button>
              </div>
            </article>
          </Card>
        ))}
      </div>
      {cursor && (
        <Button
          variant="outline"
          size="touch"
          disabled={loading}
          onClick={() => {
            loadMore(cursor);
          }}
        >
          Load more
        </Button>
      )}
      {loading && <output className="block">Loading dashboards…</output>}
      {confirming && (
        <DecisionDialog
          title="Remove this dashboard?"
          onClose={() => setConfirming(null)}
        >
          <p className="text-muted-foreground mt-[14px] mb-5">
            {confirming.title} will be deleted. Your home data and conversation
            remain unchanged.
          </p>
          <div className="flex flex-wrap justify-end gap-2">
            <Button
              variant="outline"
              size="touch"
              onClick={() => setConfirming(null)}
            >
              Cancel
            </Button>
            <Button
              size="touch"
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
