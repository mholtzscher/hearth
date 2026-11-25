import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import {
  FALLBACK_STARTER_QUESTIONS,
  useStarterQuestionSelection,
} from "./starter-questions";

const served = [
  { title: "Why is the Garage Lock offline?", prompt: "Why is it offline?" },
  { title: "Show the office temperature", prompt: "Show it today." },
  { title: "What happened overnight?", prompt: "What happened?" },
];
const newer = [
  { title: "Is the porch light on?", prompt: "Is it on?" },
  { title: "Show humidity today", prompt: "Show humidity." },
  { title: "Which doors are open?", prompt: "Which doors?" },
];

interface Props {
  empty: boolean;
  loading: boolean;
  served: typeof served | null;
  pending?: boolean;
}

const renderSelection = (initial: Props) => {
  const refresh = vi.fn<() => void>();
  const hook = renderHook(
    (props: Props) =>
      useStarterQuestionSelection({ pending: false, ...props, refresh }),
    { initialProps: initial }
  );
  return { ...hook, refresh };
};

describe(useStarterQuestionSelection, () => {
  it("offers generated questions when the empty state opens", () => {
    const { result } = renderSelection({
      empty: true,
      loading: false,
      served,
    });
    expect(result.current).toBe(served);
  });

  it("falls back when Core has none, has not answered, or failed", () => {
    for (const value of [null, []]) {
      const { result } = renderSelection({
        empty: true,
        loading: false,
        served: value,
      });
      expect(result.current).toBe(FALLBACK_STARTER_QUESTIONS);
    }
  });

  it("offers nothing until the first read answers, then chooses once", () => {
    const { result, rerender } = renderSelection({
      empty: true,
      loading: false,
      served: null,
      pending: true,
    });
    expect(result.current).toHaveLength(0);
    rerender({ empty: true, loading: false, served, pending: false });
    expect(result.current).toBe(served);
    rerender({ empty: true, loading: false, served: newer, pending: false });
    expect(result.current).toBe(served);
  });

  it("keeps the displayed questions while the empty state stays open", () => {
    const { result, rerender } = renderSelection({
      empty: true,
      loading: false,
      served: null,
    });
    rerender({ empty: true, loading: false, served });
    expect(result.current).toBe(FALLBACK_STARTER_QUESTIONS);
    rerender({ empty: true, loading: false, served: newer });
    expect(result.current).toBe(FALLBACK_STARTER_QUESTIONS);
  });

  it("waits for history to load before choosing", () => {
    const { result, rerender } = renderSelection({
      empty: true,
      loading: true,
      served: null,
    });
    rerender({ empty: true, loading: false, served });
    expect(result.current).toBe(served);
  });

  it("reads Core again when the empty state closes and uses newer questions next time", () => {
    const { result, rerender, refresh } = renderSelection({
      empty: true,
      loading: false,
      served,
    });
    expect(refresh).not.toHaveBeenCalled();
    act(() => rerender({ empty: false, loading: false, served }));
    expect(refresh).toHaveBeenCalledOnce();
    rerender({ empty: false, loading: false, served: newer });
    rerender({ empty: true, loading: false, served: newer });
    expect(result.current).toBe(newer);
  });

  it("does not read Core again for a conversation that was never empty", () => {
    const { rerender, refresh } = renderSelection({
      empty: false,
      loading: true,
      served,
    });
    rerender({ empty: false, loading: false, served });
    expect(refresh).not.toHaveBeenCalled();
  });
});
