# Merge-risk Flue prototype (disposable)

Contained prototype answering: can a Flue `flue run` agent apply the vendored
`merge-risk` skill in GitHub Actions on PR open and post the result?

All files live in this one dir (`prototype-merge-risk/`). Nothing is wired into
`.github/workflows/` or the repo root; the workflow here is a DRAFT copy.

## Contents

- `src/agents/merge-risk.ts` — agent: model + `local({env:{GH_TOKEN, OPENCODE_API_KEY}})` sandbox,
  instruction to apply the `merge-risk` skill and publish via the
  `upsert-merge-risk-comment` harness tool (typed `{repo, prNumber, body}`;
  in-process GitHub API call, updates own prior comment by marker, no
  stdout parsing).
- `.agents/skills/merge-risk/SKILL.md` — body verbatim from
  `warpdotdev/common-skills` (source URL in frontmatter); frontmatter
  `metadata` flattened to string-to-string because Flue rejects nested
  mappings.
- `github-workflow.merge-risk-prototype.yml` — DRAFT workflow (`pull_request:
  [opened, synchronize, reopened]`, `pull-requests: write`); copy to
  `.github/workflows/` only to activate.
- `package.json` — deps `@flue/runtime`, `valibot`, dev `@flue/cli`.
- `AGENTS.md` — minimal prototype context.

## Try (needs OPENCODE_API_KEY + GH_TOKEN in env; no secrets post anything)

```sh
cd prototype-merge-risk
npm install
OPENCODE_API_KEY=... GH_TOKEN=... npx flue run src/agents/merge-risk.ts --message "Apply the merge-risk skill to PR #<n> in <owner/repo>"
```

With plain `flue run`, the summary line streams to the log; in CI the agent
publishes the comment itself through the tool (no `jq`/`gh pr comment` step).

## Observable comparison (pass criteria)

- `flue run` loads the skill (no `Skipping invalid workspace skill`), calls
  `upsert-merge-risk-comment`, and replies with posted/updated + URL.
- Exactly one bot comment per PR carries `<!-- flue-merge-risk-bot -->`;
  re-runs PATCH it instead of posting anew.

## Disposal

Delete this dir after the decision, or promote only via a separate approved
spec. This prototype authorizes no merge and no production workflow install.
