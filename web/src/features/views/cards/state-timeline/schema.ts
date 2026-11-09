import { z } from "zod";
import { seriesWindows, textBindingSchema, wordsSchema } from "../../bindings/schema";
import { plainText } from "../../catalog/primitives";

/** The most rows one timeline shows; more fields belong in another timeline. */
export const MAX_TIMELINE_ROWS = 12;
export const timelineStyles = ["lanes", "ledger", "feed"] as const;
export type TimelineStyle = (typeof timelineStyles)[number];
export const stateTimelineSchema = z.strictObject({
  title: plainText(120),
  style: z.enum(timelineStyles).describe(
    "lanes: one shared time axis for comparing rows. ledger: each row's current value and totals above a strip. feed: a merged list of changes, newest first.",
  ),
  window: z.enum(seriesWindows),
  rows: z.array(z.strictObject({
    source: textBindingSchema, label: plainText(120).optional(), words: wordsSchema.optional(),
  })).min(1).max(MAX_TIMELINE_ROWS),
});
