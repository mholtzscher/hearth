import { useModel, useSandbox, useTool } from '@flue/runtime';
import { local } from '@flue/runtime/node';
import * as v from 'valibot';

// Marker identifying comments owned by this bot. The tool appends it;
// update-in-place matches on it.
const MARKER = '<!-- flue-merge-risk-bot -->';

async function github(
  path: string,
  init: { method?: string; body?: unknown } = {},
): Promise<any> {
  const token = process.env.GH_TOKEN;
  if (!token) {
    throw new Error('GH_TOKEN is not set');
  }
  const res = await fetch(`https://api.github.com${path}`, {
    method: init.method ?? 'GET',
    headers: {
      Authorization: `Bearer ${token}`,
      Accept: 'application/vnd.github+json',
      'Content-Type': 'application/json',
      'User-Agent': 'flue-merge-risk-prototype',
    },
    body: init.body === undefined ? undefined : JSON.stringify(init.body),
  });
  if (!res.ok) {
    throw new Error(`GitHub API ${init.method ?? 'GET'} ${path} failed: ${res.status}`);
  }
  return res.json();
}

// Disposable prototype tool: post the assessment, updating the bot's own
// prior comment when one exists. In-process fetch (no shell quoting of
// model-generated markdown).
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
  useTool({
    name: 'upsert-merge-risk-comment',
    description:
      'Publish the merge-risk assessment on a PR. Updates the existing bot comment (matched by marker) or posts a new one.',
    input: v.object({
      repo: v.string(),
      prNumber: v.number(),
      body: v.string(),
    }),
    harness: true,
    async run({ data }) {
      const fullBody = `${data.body.trim()}\n\n${MARKER}\n`;
      const comments = await github(
        `/repos/${data.repo}/issues/${data.prNumber}/comments?per_page=100`,
      );
      const existing = (comments as Array<{ id: number; body?: string }>).find(
        (c) => typeof c.body === 'string' && c.body.includes(MARKER),
      );
      if (existing) {
        const updated = await github(
          `/repos/${data.repo}/issues/comments/${existing.id}`,
          { method: 'PATCH', body: { body: fullBody } },
        );
        return { output: { updated: true, url: updated.html_url } };
      }
      const created = await github(
        `/repos/${data.repo}/issues/${data.prNumber}/comments`,
        { method: 'POST', body: { body: fullBody } },
      );
      return { output: { updated: false, url: created.html_url } };
    },
  });
  return [
    'When given a pull request number, apply the `merge-risk` skill to it.',
    'Fetch the PR diff and metadata with `gh pr view` / `gh pr diff`.',
    'Then call the `upsert-merge-risk-comment` tool with repo, prNumber, and the skill-shaped markdown body (no marker needed).',
    'Reply with one line only: whether the comment was posted or updated, plus its URL.',
  ].join(' ');
}
