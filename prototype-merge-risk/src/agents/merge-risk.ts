import { useModel, useSandbox } from '@flue/runtime';
import { local } from '@flue/runtime/node';

// Disposable prototype agent: applies the vendored `merge-risk` skill to one PR.
// The PR number arrives in the `--message` text from the workflow (or local CLI).
export function MergeRisk() {
  useModel('opencode-go/deepseek-v4.1-flash');
  useSandbox(
    local({
      env: {
        GH_TOKEN: process.env.GH_TOKEN,
        OPENCODE_API_KEY: process.env.OPENCODE_API_KEY,
      },
    }),
  );
  return [
    'When given a pull request number, apply the `merge-risk` skill to it.',
    'Fetch the PR diff and metadata with `gh pr view` / `gh pr diff`.',
    'Output ONLY the skill-shaped markdown comment, starting with `**Merge risk:`.',
    'No extra prose outside the comment body.',
  ].join(' ');
}
