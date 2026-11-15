import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { ReadingRow } from "./reading";

describe("Reading row layout", () => {
  afterEach(cleanup);

  it("keeps the default reading row and exposes direct sibling slots", () => {
    const { rerender } = render(
      <ul>
        <ReadingRow name="Air" value="dimmed">
          7
        </ReadingRow>
      </ul>
    );
    const row = screen.getByRole("listitem");
    expect(
      [...row.children].map((child) => {
        if (!(child instanceof HTMLElement)) {
          throw new TypeError("Expected an HTML reading slot");
        }
        return child.dataset.slot;
      })
    ).toStrictEqual(["reading-name", "reading-value"]);
    expect(
      row.querySelector<HTMLElement>('[data-slot="reading-value"]')?.dataset
        .value
    ).toBe("dimmed");
    rerender(
      <ul>
        <ReadingRow
          name="Air"
          className="border-border border-t"
          beforeValue={<span>Good</span>}
          afterValue={<span>Replace filter</span>}
        >
          7
        </ReadingRow>
      </ul>
    );
    expect([...row.children].map((child) => child.textContent)).toStrictEqual([
      "Air",
      "Good",
      "7",
      "Replace filter",
    ]);
    expect(row.classList.contains("border-t")).toBeTruthy();
  });
});
