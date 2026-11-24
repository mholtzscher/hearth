import { ComposerPrimitive } from "@assistant-ui/react";
import { ArrowUpIcon, PanelsTopLeftIcon, XIcon } from "lucide-react";
import type { RefObject } from "react";

import { Button } from "../../components/button";

// The view or Automation on the canvas travels with the next message; the chip shows it and closes it like the canvas's own Close.
// Its revision and saved state stay in the canvas header, which is beside the composer or one tap away.
const SelectedContextChip = ({
  kind,
  title,
  closeDisabled,
  onShow,
  onClose,
}: {
  kind: string;
  title: string;
  closeDisabled: boolean;
  onShow: () => void;
  onClose: () => void;
}) => (
  <fieldset
    className="border-violet/35 text-label text-violet-text -ms-2 flex max-w-[calc(100%+8px)] min-w-0 justify-self-start rounded-md border [min-inline-size:0]"
    aria-label={`Selected ${kind}`}
  >
    <Button
      variant="ghost"
      size="touch"
      type="button"
      className="text-violet-text hover:bg-violet-soft min-w-0 shrink justify-start gap-2 ps-[7px] pe-1 text-left"
      aria-label={`Show selected ${kind}: ${title}`}
      title={title}
      onClick={onShow}
    >
      <PanelsTopLeftIcon aria-hidden="true" />
      <span className="min-w-0 truncate">{title}</span>
    </Button>
    <Button
      variant="ghost"
      size="icon-touch"
      type="button"
      className="hover:bg-violet-soft"
      aria-label={`Close selected ${kind}`}
      title={`Close ${kind}`}
      disabled={closeDisabled}
      onClick={onClose}
    >
      <XIcon aria-hidden="true" />
    </Button>
  </fieldset>
);

export interface ComposerProps {
  prompt: RefObject<HTMLTextAreaElement | null>;
  busy: boolean;
  loading: boolean;
  failedPrompt: string;
  automationName: string | undefined;
  viewTitle: string | undefined;
  saving: boolean;
  editorValid: boolean;
  onShow: () => void;
  onCloseDraft: () => void;
}

export const ConversationComposer = ({
  prompt,
  busy,
  loading,
  failedPrompt,
  automationName,
  viewTitle,
  saving,
  editorValid,
  onShow,
  onCloseDraft,
}: ComposerProps) => {
  let selected: { kind: string; title: string } | undefined;
  if (automationName !== undefined) {
    selected = { kind: "Automation", title: automationName };
  } else if (viewTitle !== undefined) {
    selected = { kind: "view", title: viewTitle };
  }
  const handleCloseDraft = () => {
    onCloseDraft();
  };
  return (
    <ComposerPrimitive.Root className="max-tablet:pb-[max(12px,var(--safe-bottom,env(safe-area-inset-bottom)))] shrink-0 px-[var(--pad)] pt-3 pb-5">
      <div className="rounded-card border-gray-4 bg-card focus-within:border-ring flex flex-col border p-2">
        <label className="sr-only" htmlFor="hearth-prompt">
          Ask Hearth
        </label>
        <ComposerPrimitive.Input
          className="text-body text-foreground caret-violet placeholder:text-muted-foreground block min-h-11 w-full resize-none overflow-y-auto border-0 bg-transparent px-2 py-2.5 leading-normal outline-none placeholder:opacity-100 focus-visible:outline-none"
          ref={prompt}
          id="hearth-prompt"
          minRows={1}
          maxRows={6}
          maxLength={4000}
          disabled={busy || loading}
          submitMode="enter"
          cancelOnEscape={false}
          addAttachmentOnPaste={false}
          placeholder="Ask about your home, or ask for a view…"
        />
        {/* The selected view and any note share Send's row rather than adding rows of their own. */}
        <div className="flex items-center gap-3">
          <div className="grid min-w-0 flex-1 gap-0.5 px-2">
            {selected && (
              <SelectedContextChip
                {...selected}
                closeDisabled={saving}
                onShow={onShow}
                onClose={handleCloseDraft}
              />
            )}
            {!editorValid && (
              <p className="text-label text-muted-foreground">
                Fix or discard invalid draft edits before sending
              </p>
            )}
          </div>
          <ComposerPrimitive.Send asChild>
            <Button
              size="touch"
              className="ms-auto min-w-11 gap-1"
              aria-label={failedPrompt ? "Retry message" : "Send message"}
            >
              {failedPrompt && "Retry"}
              <ArrowUpIcon className="size-[18px]" aria-hidden="true" />
            </Button>
          </ComposerPrimitive.Send>
        </div>
      </div>
    </ComposerPrimitive.Root>
  );
};
