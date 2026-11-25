import { act, renderHook, waitFor } from "@testing-library/react";
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
  enabled?: boolean;
}

const renderSelection = (
  initial: Props,
  regenerate = vi.fn<() => Promise<readonly (typeof served)[number][]>>()
) => {
  const refresh = vi.fn<() => void>();
  const hook = renderHook(
    (props: Props) =>
      useStarterQuestionSelection({
        pending: false,
        enabled: true,
        ...props,
        refresh,
        regenerate,
      }),
    { initialProps: initial }
  );
  return { ...hook, refresh, regenerate };
};

describe(useStarterQuestionSelection, () => {
  it("offers generated questions when the empty state opens", () => {
    const { result } = renderSelection({
      empty: true,
      loading: false,
      served,
    });
    expect(result.current.questions).toBe(served);
  });

  it("falls back when Core has none, has not answered, or failed", () => {
    for (const value of [null, []]) {
      const { result } = renderSelection({
        empty: true,
        loading: false,
        served: value,
      });
      expect(result.current.questions).toBe(FALLBACK_STARTER_QUESTIONS);
    }
  });

  it("offers nothing until the first read answers, then chooses once", () => {
    const { result, rerender } = renderSelection({
      empty: true,
      loading: false,
      served: null,
      pending: true,
    });
    expect(result.current.questions).toHaveLength(0);
    rerender({ empty: true, loading: false, served, pending: false });
    expect(result.current.questions).toBe(served);
    rerender({ empty: true, loading: false, served: newer, pending: false });
    expect(result.current.questions).toBe(served);
  });

  it("keeps the displayed questions while the empty state stays open", () => {
    const { result, rerender } = renderSelection({
      empty: true,
      loading: false,
      served: null,
    });
    rerender({ empty: true, loading: false, served });
    expect(result.current.questions).toBe(FALLBACK_STARTER_QUESTIONS);
    rerender({ empty: true, loading: false, served: newer });
    expect(result.current.questions).toBe(FALLBACK_STARTER_QUESTIONS);
  });

  it("waits for history to load before choosing", () => {
    const { result, rerender } = renderSelection({
      empty: true,
      loading: true,
      served: null,
    });
    rerender({ empty: true, loading: false, served });
    expect(result.current.questions).toBe(served);
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
    expect(result.current.questions).toBe(newer);
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

  it("replaces the shown questions with newly generated ones on request", async () => {
    const regenerate = vi.fn<() => Promise<typeof newer>>(() =>
      Promise.resolve(newer)
    );
    const { result } = renderSelection(
      { empty: true, loading: false, served },
      regenerate
    );
    expect(result.current.canRegenerate).toBeTruthy();
    await act(() => result.current.regenerate());
    expect(regenerate).toHaveBeenCalledOnce();
    expect(result.current.questions).toBe(newer);
    expect(result.current.regenerating).toBeFalsy();
    expect(result.current.regenerateFailed).toBeFalsy();
  });

  it("keeps the shown questions and reports a failed request", async () => {
    const regenerate = vi.fn<() => Promise<typeof newer>>(() =>
      Promise.reject(new Error("502"))
    );
    const { result } = renderSelection(
      { empty: true, loading: false, served },
      regenerate
    );
    await act(() => result.current.regenerate());
    expect(result.current.questions).toBe(served);
    expect(result.current.regenerateFailed).toBeTruthy();
  });

  it("ignores an answer that arrives after the conversation started", async () => {
    const answer = Promise.withResolvers<typeof newer>();
    const regenerate = vi.fn<() => Promise<typeof newer>>(() => answer.promise);
    const { result, rerender } = renderSelection(
      { empty: true, loading: false, served },
      regenerate
    );
    let request: Promise<void> = Promise.resolve();
    act(() => {
      request = result.current.regenerate();
    });
    expect(result.current.regenerating).toBeTruthy();
    act(() => rerender({ empty: false, loading: false, served }));
    await act(async () => {
      answer.resolve(newer);
      await request;
    });
    rerender({ empty: true, loading: false, served });
    await waitFor(() => expect(result.current.questions).toBe(served));
    expect(result.current.regenerating).toBeFalsy();
  });

  it("offers no regeneration when Core does not generate questions or none are shown", () => {
    const off = renderSelection({
      empty: true,
      loading: false,
      served: null,
      enabled: false,
    });
    expect(off.result.current.canRegenerate).toBeFalsy();
    const hidden = renderSelection({
      empty: true,
      loading: false,
      served: null,
      pending: true,
    });
    expect(hidden.result.current.canRegenerate).toBeFalsy();
  });
});
