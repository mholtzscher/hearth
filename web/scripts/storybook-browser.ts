import { accessSync, constants } from "node:fs";
import path from "node:path";

export const resolveChromium = (
  env: NodeJS.ProcessEnv = process.env
): string | undefined => {
  if (env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH) {
    return env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;
  }
  for (const dir of (env.PATH ?? "").split(path.delimiter)) {
    for (const name of ["chromium", "chromium-browser"]) {
      const executable = path.join(dir, name);
      try {
        accessSync(executable, constants.X_OK);
        return executable;
      } catch {
        /* Try the next PATH entry. */
      }
    }
  }
  return undefined;
};
