export type StoryCheckResult = { status: string | null; errors: string[] };
type Channel = { on: (event: string, listener: (value: { status?: string }) => void) => void };
export type StoryObserverScope = {
  __HEARTH_STORY_CHECK__: StoryCheckResult;
  __STORYBOOK_ADDONS_CHANNEL__: Channel | undefined;
};

// Installed before preview scripts: fast stories can finish before goto resolves.
// Keep this function self-contained so Playwright can serialize it into the page.
export function installStoryObserver(target?: StoryObserverScope) {
  const scope = target ?? globalThis as unknown as StoryObserverScope;
  const result = { status: null as string | null, errors: [] as string[] };
  scope.__HEARTH_STORY_CHECK__ = result;
  let channel: Channel | undefined;
  Object.defineProperty(scope, "__STORYBOOK_ADDONS_CHANNEL__", {
    configurable: true,
    get: () => channel,
    set(value: Channel) {
      channel = value;
      for (const event of [
        "playFunctionThrewException", "storyErrored", "storyThrewException",
        "unhandledErrorsWhilePlaying", "storyMissing", "configError",
      ]) {
        value.on(event, () => result.errors.push(event));
      }
      value.on("storyFinished", event => { result.status = event.status ?? "unknown"; });
    },
  });
}
