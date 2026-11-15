import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { ReadingRow } from "./Reading";

afterEach(cleanup);

it("keeps the default reading row and exposes direct sibling slots", () => {
  const { rerender } = render(<ul><ReadingRow name="Air" value="dimmed">7</ReadingRow></ul>);
  const row = screen.getByRole("listitem");
  expect([...row.children].map(child => child.getAttribute("data-slot"))).toEqual(["reading-name", "reading-value"]);
  expect(row.querySelector('[data-slot="reading-value"]')?.getAttribute("data-value")).toBe("dimmed");
  rerender(<ul><ReadingRow name="Air" className="border-t border-border" beforeValue={<span>Good</span>} afterValue={<span>Replace filter</span>}>7</ReadingRow></ul>);
  expect([...row.children].map(child => child.textContent)).toEqual(["Air", "Good", "7", "Replace filter"]);
  expect(row.classList.contains("border-t")).toBe(true);
});
