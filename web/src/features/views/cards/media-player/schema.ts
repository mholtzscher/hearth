import { z } from "zod";

import { entityID, plainText } from "../../catalog/primitives";

export const mediaPlayerLayouts = ["card", "tile"] as const;

/** A speaker's whole State: playback, media, playhead, and volume. */
export const mediaPlayerBindingSchema = z
  .strictObject({
    kind: z.literal("entity-state"),
    entity_id: entityID,
    entity_type: z.literal("hearth.mediaplayer/v1"),
    field: z.literal("").optional(),
  })
  .meta({ id: "MediaPlayerSource" });
export type MediaPlayerSource = z.infer<typeof mediaPlayerBindingSchema>;

export const mediaPlayerSchema = z.strictObject({
  layout: z
    .enum(mediaPlayerLayouts)
    .describe(
      "card: artwork, track, progress, transport, and volume. tile: a whole-card play/pause press for grids of quick switches. Omitted means card."
    )
    .optional(),
  label: plainText(120).optional(),
  source: mediaPlayerBindingSchema,
});
export type MediaPlayerProps = z.infer<typeof mediaPlayerSchema>;
