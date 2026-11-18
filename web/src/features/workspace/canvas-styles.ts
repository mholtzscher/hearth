/** Shared layout for View and Automation canvas headers. */
export const canvasHeader =
  "mb-5 grid grid-cols-[minmax(0,1fr)_auto] [grid-template-areas:'title_window'_'actions_actions'] items-start gap-x-4 gap-y-3 border-b border-border pb-4 @min-[1000px]/canvas:grid-cols-[minmax(0,1fr)_auto_auto] @min-[1000px]/canvas:[grid-template-areas:'title_actions_window']";
export const canvasTitle = "min-w-0 [grid-area:title]";
export const canvasSubtitle =
  "mt-1 text-label leading-normal text-muted-foreground";
export const canvasToolbar =
  "flex flex-wrap justify-between gap-x-4 gap-y-2 [grid-area:actions] @min-[1000px]/canvas:self-center";
export const canvasToolbarGroup = "flex flex-wrap items-center gap-2";
export const canvasWindowActions = "-mt-1 flex gap-1 [grid-area:window]";
export const phoneSaveBar =
  "max-tablet:fixed max-tablet:inset-x-0 max-tablet:bottom-0 max-tablet:z-30 max-tablet:flex-nowrap max-tablet:justify-end max-tablet:border-t max-tablet:border-border max-tablet:bg-background max-tablet:px-5 max-tablet:pt-3 max-tablet:pb-[max(12px,var(--safe-bottom,env(safe-area-inset-bottom)))]";
export const phoneSaveInset =
  "max-tablet:pb-[calc(96px+env(safe-area-inset-bottom))]";
export const canvasInner =
  "@container/canvas mx-auto max-w-[1080px] px-7 pt-[22px] pb-10 max-tablet:px-5 max-tablet:pt-4 max-tablet:pb-7";
