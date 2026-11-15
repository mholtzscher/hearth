import { createCn } from "cn/config";

/** Custom @theme names: cn must distinguish text sizes from text colours. */
export const TEXT_TOKENS = [
  "reading", "reading-narrow", "headline", "title", "body", "label", "compact-reading", "caption",
] as const;
export const RADIUS_TOKENS = ["card"] as const;

export const cn = createCn({
  extend: {
    theme: { radius: [...RADIUS_TOKENS] },
    classGroups: {
      "font-size": [{ text: [...TEXT_TOKENS] }],
    },
  },
});
